// SPDX-License-Identifier: AGPL-3.0-only
//! Experimental Android relay. No model weights or GGML operators live here.
//! Keep the exact existing HTTP/1.1 CONNECT + marker + raw GGML wire protocol.
use jni::{
    objects::{JByteArray, JObject, JString},
    sys::{jboolean, jint, jlong, jstring, JNI_FALSE, JNI_TRUE},
    JNIEnv,
};
use rustls::{
    client::danger::{HandshakeSignatureValid, ServerCertVerified, ServerCertVerifier},
    crypto::{verify_tls12_signature, verify_tls13_signature, WebPkiSupportedAlgorithms},
    pki_types::{CertificateDer, ServerName, UnixTime},
    ClientConfig, DigitallySignedStruct, Error as TlsError, SignatureScheme,
};
use std::{
    io,
    sync::{
        atomic::{AtomicBool, AtomicU64, AtomicUsize, Ordering},
        Arc, Mutex, OnceLock,
    },
    time::Duration,
};
use tokio::{
    io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt},
    net::{TcpStream, UnixStream},
    sync::{watch, Notify},
    task::JoinSet,
    time::{sleep, timeout},
};
use tokio_rustls::TlsConnector;

mod affinity;

const MAX_SLOTS: usize = 8;
const STANDBY: usize = 2;
const BUF: usize = 32 * 1024;

#[derive(Debug)]
struct ExactPin {
    der: CertificateDer<'static>,
    algorithms: WebPkiSupportedAlgorithms,
}
impl ServerCertVerifier for ExactPin {
    fn verify_server_cert(
        &self,
        peer: &CertificateDer<'_>,
        _: &[CertificateDer<'_>],
        _: &ServerName<'_>,
        _: &[u8],
        _: UnixTime,
    ) -> Result<ServerCertVerified, TlsError> {
        if peer.as_ref() == self.der.as_ref() {
            Ok(ServerCertVerified::assertion())
        } else {
            Err(TlsError::InvalidCertificate(
                rustls::CertificateError::ApplicationVerificationFailure,
            ))
        }
    }

    // The pin alone is insufficient: verify the actual handshake signature
    // to prove the peer owns the key of the enrolled certificate.
    fn verify_tls12_signature(
        &self, message: &[u8], cert: &CertificateDer<'_>, sig: &DigitallySignedStruct,
    ) -> Result<HandshakeSignatureValid, TlsError> {
        verify_tls12_signature(message, cert, sig, &self.algorithms)
    }
    fn verify_tls13_signature(
        &self, message: &[u8], cert: &CertificateDer<'_>, sig: &DigitallySignedStruct,
    ) -> Result<HandshakeSignatureValid, TlsError> {
        verify_tls13_signature(message, cert, sig, &self.algorithms)
    }
    fn supported_verify_schemes(&self) -> Vec<SignatureScheme> {
        self.algorithms.supported_schemes()
    }
}

struct Config {
    host: String,
    port: u16,
    request: Vec<u8>,
    socket: String,
    server_name: ServerName<'static>,
    efficient_cores: bool,
    tls: TlsConnector,
}

fn safe_header(value: &str) -> bool {
    !value.is_empty() && value.bytes().all(|b| (33..=126).contains(&b))
}
fn config(host: String, port: u16, node: String, token: String, cert: Vec<u8>, socket: String, efficient_cores: bool)
    -> Result<Config, &'static str>
{
    if !safe_header(&host) || !safe_header(&node) || !safe_header(&token)
        || !(32..=128).contains(&node.len()) || !(32..=512).contains(&token.len())
        || cert.is_empty() || cert.len() > 65536 || port == 0
        || !socket.starts_with('/') || socket.len() > 100
    {
        return Err("Invalid Rust RPC enrollment");
    }
    let host = host.trim_start_matches('[').trim_end_matches(']').to_owned();
    let server_name = ServerName::try_from(host.clone()).map_err(|_| "Invalid Host name")?;
    let authority = if host.contains(':') { format!("[{host}]:{port}") } else { format!("{host}:{port}") };
    let provider = rustls::crypto::ring::default_provider();
    let verifier = ExactPin {
        der: CertificateDer::from(cert),
        algorithms: provider.signature_verification_algorithms,
    };
    let mut tls = ClientConfig::builder_with_provider(Arc::new(provider))
        .with_safe_default_protocol_versions().map_err(|_| "Invalid TLS configuration")?
        .dangerous()
        .with_custom_certificate_verifier(Arc::new(verifier))
        .with_no_client_auth();
    tls.alpn_protocols = vec![b"http/1.1".to_vec()];
    // Do not cache TLS session secrets or enable early data. Each tunnel
    // authenticates the exact currently enrolled Host.
    tls.resumption = rustls::client::Resumption::disabled();
    let request = format!("CONNECT /v1/nodes/rpc/tunnel HTTP/1.1\r\nHost: {authority}\r\nAuthorization: Bearer {token}\r\nX-Nodex-Node: {node}\r\n\r\n").into_bytes();
    Ok(Config { host, port, request, socket, server_name, efficient_cores, tls: TlsConnector::from(Arc::new(tls)) })
}

struct Control {
    stop: watch::Sender<bool>,
    drain: watch::Sender<bool>,
    claimed: AtomicUsize,
    failures: AtomicU64,
    changed: Notify,
    running: AtomicBool,
    error: Mutex<&'static str>,
}
impl Control {
    fn new() -> Self {
        let (stop, _) = watch::channel(false);
        let (drain, _) = watch::channel(false);
        Self { stop, drain, claimed: AtomicUsize::new(0), failures: AtomicU64::new(0),
            changed: Notify::new(), running: AtomicBool::new(true), error: Mutex::new("") }
    }
}
struct Claim(Arc<Control>);
impl Claim {
    fn new(c: Arc<Control>) -> Self {
        c.claimed.fetch_add(1, Ordering::AcqRel);
        c.changed.notify_one();
        Self(c)
    }
}
impl Drop for Claim {
    fn drop(&mut self) {
        self.0.claimed.fetch_sub(1, Ordering::AcqRel);
        self.0.changed.notify_one();
    }
}

#[derive(Debug)]
enum RelayError { Io(io::Error), Authentication, Capacity, Protocol }
impl From<io::Error> for RelayError {
    fn from(value: io::Error) -> Self { Self::Io(value) }
}
fn timed_out() -> io::Error { io::Error::new(io::ErrorKind::TimedOut, "RPC timeout") }

async fn connect_status<R: AsyncRead + Unpin>(r: &mut R) -> Result<(), RelayError> {
    let mut header = Vec::with_capacity(512);
    while header.len() < 8192 {
        header.push(r.read_u8().await?);
        if header.ends_with(b"\r\n\r\n") {
            let first = header.split(|b| *b == b'\r').next().ok_or(RelayError::Protocol)?;
            let first = std::str::from_utf8(first).map_err(|_| RelayError::Protocol)?;
            let mut words = first.splitn(3, ' ');
            if !matches!(words.next(), Some("HTTP/1.0" | "HTTP/1.1")) {
                return Err(RelayError::Protocol);
            }
            return match words.next() {
                Some("200") => Ok(()),
                Some("401" | "403") => Err(RelayError::Authentication),
                Some("503") => Err(RelayError::Capacity),
                _ => Err(RelayError::Protocol),
            };
        }
    }
    Err(RelayError::Protocol)
}

async fn benchmark<S: AsyncRead + AsyncWrite + Unpin>(s: &mut S) -> Result<(), RelayError> {
    let mut buffer = vec![0; 16 * 1024];
    for _ in 0..2 {
        let mut remaining = s.read_u32().await? as usize;
        if !(1..=1 << 20).contains(&remaining) { return Err(RelayError::Protocol); }
        while remaining != 0 {
            let n = remaining.min(buffer.len());
            s.read_exact(&mut buffer[..n]).await?;
            s.write_all(&buffer[..n]).await?;
            remaining -= n;
        }
        s.flush().await?;
    }
    Ok(())
}

// A single-thread executor can reuse a small bounded pool without mutexes.
// Only retain two buffers (64 KiB total); larger concurrent claims allocate
// independently and release excess immediately. No tensor data is logged.
thread_local! {
    static RELAY_POOL: std::cell::RefCell<Vec<Vec<u8>>> = const {
        std::cell::RefCell::new(Vec::new())
    };
}
struct RelayBuffer(Vec<u8>);
impl RelayBuffer {
    fn acquire() -> Self {
        Self(RELAY_POOL.with(|pool| pool.borrow_mut().pop())
            .unwrap_or_else(|| vec![0; BUF]))
    }
}
impl Drop for RelayBuffer {
    fn drop(&mut self) {
        let mut buffer = std::mem::take(&mut self.0);
        // Clear data before retaining memory across authenticated sessions.
        buffer.fill(0);
        let _ = RELAY_POOL.try_with(|pool| {
            let mut pool = pool.borrow_mut();
            if pool.len() < 2 { pool.push(buffer); }
        });
    }
}

// Two bounded transfer buffers are allocated only on claim. Split halves are
// borrowed, not cloned; no JVM ByteArray, JNI copy or tensor-sized allocation.
async fn forward<R: AsyncRead + Unpin, W: AsyncWrite + Unpin>(mut r: R, mut w: W)
    -> io::Result<()>
{
    let mut buffer = RelayBuffer::acquire();
    loop {
        let n = r.read(&mut buffer.0).await?;
        if n == 0 { return Ok(()); }
        w.write_all(&buffer.0[..n]).await?;
        w.flush().await?;
    }
}
async fn bridge<A, B>(a: A, b: B) -> io::Result<()>
where A: AsyncRead + AsyncWrite + Unpin, B: AsyncRead + AsyncWrite + Unpin
{
    let (ar, aw) = tokio::io::split(a);
    let (br, bw) = tokio::io::split(b);
    // GGML expects a complete disconnect when either relay direction closes.
    // Dropping both halves unblocks the AF_UNIX server and the Host.
    tokio::select! { result = forward(ar, bw) => result, result = forward(br, aw) => result }
}

async fn relay(cfg: &Config, c: Arc<Control>) -> Result<(), RelayError> {
    let tcp = timeout(Duration::from_secs(7), TcpStream::connect((cfg.host.as_str(), cfg.port)))
        .await.map_err(|_| timed_out())??;
    tcp.set_nodelay(true)?;
    socket2::SockRef::from(&tcp).set_keepalive(true)?;
    let mut remote = timeout(Duration::from_secs(10), cfg.tls.connect(cfg.server_name.clone(), tcp))
        .await.map_err(|_| timed_out())??;
    if let Some(alpn) = remote.get_ref().1.alpn_protocol() {
        if alpn != b"http/1.1" { return Err(RelayError::Protocol); }
    }
    timeout(Duration::from_secs(10), async {
        remote.write_all(&cfg.request).await?;
        remote.flush().await?;
        connect_status(&mut remote).await
    }).await.map_err(|_| timed_out())??;
    // No idle read timeout: Host can keep an authenticated tunnel waiting.
    match remote.read_u8().await? {
        2 => timeout(Duration::from_secs(15), benchmark(&mut remote))
            .await.map_err(|_| timed_out())?,
        1 => {
            let _claim = Claim::new(c);
            let local = timeout(Duration::from_secs(3), UnixStream::connect(&cfg.socket))
                .await.map_err(|_| timed_out())??;
            bridge(remote, local).await?;
            Ok(())
        }
        _ => Err(RelayError::Protocol),
    }
}

async fn slot(cfg: Arc<Config>, c: Arc<Control>, slot: usize) -> Result<(), &'static str> {
    let mut stop = c.stop.subscribe();
    let mut drain = c.drain.subscribe();
    let mut backoff = 1000u64;
    loop {
        if *stop.borrow() { return Ok(()); }
        if *drain.borrow() {
            tokio::select! {
                _ = stop.wait_for(|v| *v) => return Ok(()),
                _ = drain.wait_for(|v| !*v) => continue,
            }
        }
        let result = tokio::select! {
            _ = stop.wait_for(|v| *v) => return Ok(()),
            result = relay(&cfg, c.clone()) => result,
        };
        let mut ceiling = 10_000;
        match result {
            Ok(()) => backoff = 1000,
            Err(RelayError::Authentication) => return Err("Host denied authenticated Rust RPC tunnel"),
            Err(e) => {
                if let RelayError::Capacity = e { ceiling = 30_000; }
                c.failures.fetch_add(1, Ordering::Relaxed);
                // Do not log raw network errors, credentials, or peer content.
                if let RelayError::Io(ref err) = e { let _ = err.kind(); }
            }
        }
        // Stagger reconnects by slot without a per-slot RNG or polling thread.
        let delay = backoff + (slot as u64 * 137) % (backoff / 4 + 1);
        tokio::select! {
            _ = stop.wait_for(|v| *v) => return Ok(()),
            _ = sleep(Duration::from_millis(delay)) => {}
        }
        backoff = (backoff * 2).min(ceiling);
    }
}

fn desired_slots(claimed: usize) -> usize { claimed.saturating_add(STANDBY).min(MAX_SLOTS) }
async fn run(cfg: Arc<Config>, c: Arc<Control>) -> Result<(), &'static str> {
    let mut tasks = JoinSet::new();
    let mut started = 0usize;
    let mut stop = c.stop.subscribe();
    loop {
        if *stop.borrow() { break; }
        while started < desired_slots(c.claimed.load(Ordering::Acquire)) {
            tasks.spawn(slot(cfg.clone(), c.clone(), started));
            started += 1;
        }
        tokio::select! {
            _ = stop.wait_for(|v| *v) => break,
            _ = c.changed.notified() => {},
            result = tasks.join_next() => match result {
                Some(Ok(Err(e))) => return Err(e),
                Some(Ok(Ok(()))) if *c.stop.borrow() => break,
                _ => return Err("Rust RPC slot exited unexpectedly"),
            }
        }
    }
    tasks.shutdown().await;
    Ok(())
}

static STATE: OnceLock<Mutex<Option<Arc<Control>>>> = OnceLock::new();
fn state() -> &'static Mutex<Option<Arc<Control>>> { STATE.get_or_init(|| Mutex::new(None)) }
fn current() -> Option<Arc<Control>> { state().lock().ok()?.clone() }

fn start(cfg: Config) -> Result<(), &'static str> {
    let mut guard = state().lock().map_err(|_| "Rust RPC state unavailable")?;
    if guard.as_ref().is_some_and(|c| c.running.load(Ordering::Acquire)) {
        return Err("Rust RPC already running");
    }
    let c = Arc::new(Control::new());
    let worker = c.clone();
    std::thread::Builder::new().name("nodexlm-rust-rpc".into()).spawn(move || {
        if cfg.efficient_cores { affinity::prefer_efficient(); }
        let result = match tokio::runtime::Builder::new_current_thread().enable_all().build() {
            Ok(rt) => rt.block_on(run(Arc::new(cfg), worker.clone())),
            Err(_) => Err("Cannot initialize Rust RPC executor"),
        };
        if let Err(e) = result {
            if let Ok(mut error) = worker.error.lock() { *error = e; }
        }
        worker.running.store(false, Ordering::Release);
    }).map_err(|_| "Cannot spawn Rust RPC executor")?;
    *guard = Some(c);
    Ok(())
}

fn java_string(env: &mut JNIEnv<'_>, value: &JString<'_>) -> Result<String, &'static str> {
    env.get_string(value).map(|s| s.into()).map_err(|_| "Invalid JNI string")
}
fn response(env: &mut JNIEnv<'_>, s: &str) -> jstring {
    env.new_string(s).map(|s| s.into_raw()).unwrap_or(std::ptr::null_mut())
}
#[no_mangle]
pub extern "system" fn Java_dev_nodexlm_app_NativeRustRpc_start(
    mut env: JNIEnv<'_>, _: JObject<'_>, host: JString<'_>, port: jint,
    node: JString<'_>, token: JString<'_>, cert: JByteArray<'_>, socket: JString<'_>, efficient: jboolean,
) -> jstring {
    let result = (|| {
        let host = java_string(&mut env, &host)?;
        let node = java_string(&mut env, &node)?;
        let token = java_string(&mut env, &token)?;
        let socket = java_string(&mut env, &socket)?;
        let cert = env.convert_byte_array(&cert).map_err(|_| "Invalid JNI certificate")?;
        let port = u16::try_from(port).map_err(|_| "Invalid Host port")?;
        start(config(host, port, node, token, cert, socket, efficient != JNI_FALSE)?)
    })();
    response(&mut env, result.err().unwrap_or(""))
}
#[no_mangle]
pub extern "system" fn Java_dev_nodexlm_app_NativeRustRpc_stop(_: JNIEnv<'_>, _: JObject<'_>) {
    if let Some(c) = current() { c.stop.send_replace(true); }
}
#[no_mangle]
pub extern "system" fn Java_dev_nodexlm_app_NativeRustRpc_setDraining(
    _: JNIEnv<'_>, _: JObject<'_>, value: jboolean,
) {
    if let Some(c) = current() { c.drain.send_replace(value != JNI_FALSE); }
}
#[no_mangle]
pub extern "system" fn Java_dev_nodexlm_app_NativeRustRpc_isRunning(_: JNIEnv<'_>, _: JObject<'_>) -> jboolean {
    if current().is_some_and(|c| c.running.load(Ordering::Acquire)) { JNI_TRUE } else { JNI_FALSE }
}
#[no_mangle]
pub extern "system" fn Java_dev_nodexlm_app_NativeRustRpc_claimed(_: JNIEnv<'_>, _: JObject<'_>) -> jlong {
    current().map_or(0, |c| c.claimed.load(Ordering::Acquire) as jlong)
}
#[no_mangle]
pub extern "system" fn Java_dev_nodexlm_app_NativeRustRpc_failures(_: JNIEnv<'_>, _: JObject<'_>) -> jlong {
    current().map_or(0, |c| c.failures.load(Ordering::Relaxed) as jlong)
}
#[no_mangle]
pub extern "system" fn Java_dev_nodexlm_app_NativeRustRpc_failure(mut env: JNIEnv<'_>, _: JObject<'_>) -> jstring {
    let error = current().and_then(|c| c.error.lock().ok().map(|e| *e)).unwrap_or("");
    response(&mut env, error)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn relay_pool_retains_at_most_two_clean_buffers() {
        RELAY_POOL.with(|pool| pool.borrow_mut().clear());
        let mut buffers: Vec<_> = (0..4).map(|_| RelayBuffer::acquire()).collect();
        buffers[0].0[0] = 255;
        drop(buffers);
        RELAY_POOL.with(|pool| {
            let pool = pool.borrow();
            assert_eq!(pool.len(), 2);
            assert!(pool.iter().all(|b| b.iter().all(|v| *v == 0)));
        });
    }

    #[test]
    fn pool_is_bounded() {
        assert_eq!(desired_slots(0), 2);
        assert_eq!(desired_slots(1), 3);
        assert_eq!(desired_slots(usize::MAX), 8);
    }
    #[test]
    fn cert_pin_is_exact() {
        let pin = ExactPin { der: CertificateDer::from(vec![1,2,3]),
            algorithms: rustls::crypto::ring::default_provider().signature_verification_algorithms };
        let name = ServerName::try_from("127.0.0.1").unwrap();
        let now = UnixTime::since_unix_epoch(Duration::from_secs(0));
        assert!(pin.verify_server_cert(&CertificateDer::from(vec![1,2,3]), &[], &name, &[], now).is_ok());
        assert!(pin.verify_server_cert(&CertificateDer::from(vec![1,2,4]), &[], &name, &[], now).is_err());
    }
    #[tokio::test(flavor = "current_thread")]
    async fn status_keeps_claim_and_tensor_bytes() {
        let data = b"HTTP/1.1 200 OK\r\n\r\n\x01\x00\xff";
        let mut read = &data[..];
        connect_status(&mut read).await.unwrap();
        assert_eq!(read, b"\x01\x00\xff");
        let mut denied = &b"HTTP/1.1 401 Unauthorized\r\n\r\n"[..];
        assert!(matches!(connect_status(&mut denied).await, Err(RelayError::Authentication)));
        let mut capacity = &b"HTTP/1.1 503 Busy\r\n\r\n"[..];
        assert!(matches!(connect_status(&mut capacity).await, Err(RelayError::Capacity)));
        let mut oversized = &vec![b'x'; 8192][..];
        assert!(matches!(connect_status(&mut oversized).await, Err(RelayError::Protocol)));
    }
    #[tokio::test(flavor = "current_thread")]
    async fn benchmark_echo_and_limit() {
        let (mut host, mut guest) = tokio::io::duplex(32);
        let g = tokio::spawn(async move { benchmark(&mut guest).await });
        for body in [b"abcdef".as_slice(), b"ghij".as_slice()] {
            host.write_u32(body.len() as u32).await.unwrap();
            host.write_all(body).await.unwrap();
            let mut out = vec![0; body.len()];
            host.read_exact(&mut out).await.unwrap();
            assert_eq!(out, body);
        }
        g.await.unwrap().unwrap();
    }
    #[tokio::test(flavor = "current_thread")]
    async fn relay_preserves_binary_and_cancels_on_eof() {
        let (mut host, remote) = tokio::io::duplex(32);
        let (local, mut native) = tokio::io::duplex(32);
        let task = tokio::spawn(bridge(remote, local));
        host.write_all(b"\x00\xffabc").await.unwrap();
        let mut req = [0;5];
        native.read_exact(&mut req).await.unwrap();
        assert_eq!(&req, b"\x00\xffabc");
        native.write_all(b"ok").await.unwrap();
        let mut out = [0;2];
        host.read_exact(&mut out).await.unwrap();
        assert_eq!(&out,b"ok");
        drop(host);
        timeout(Duration::from_secs(1), task).await.unwrap().unwrap().unwrap();
        assert_eq!(native.read(&mut out).await.unwrap(), 0);
    }
}
