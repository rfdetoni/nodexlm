// SPDX-License-Identifier: AGPL-3.0-only
//! Bounded deflate-v1 frames. Use the platform zlib (NDK and Linux), not a
//! second network protocol or a large new Rust dependency.
use libc::{c_char, c_int, c_uint, c_ulong, c_void};
use std::{io, mem};
use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt};

const MAX: usize = 64 * 1024;
const MIN_COMPRESS: usize = 16 * 1024;
const Z_OK: c_int = 0;
const Z_STREAM_END: c_int = 1;
const Z_FINISH: c_int = 4;

#[repr(C)]
struct ZStream {
    next_in: *mut u8,
    avail_in: c_uint,
    total_in: c_ulong,
    next_out: *mut u8,
    avail_out: c_uint,
    total_out: c_ulong,
    msg: *mut c_char,
    state: *mut c_void,
    zalloc: Option<unsafe extern "C" fn(*mut c_void, c_uint, c_uint) -> *mut c_void>,
    zfree: Option<unsafe extern "C" fn(*mut c_void, *mut c_void)>,
    opaque: *mut c_void,
    data_type: c_int,
    adler: c_ulong,
    reserved: c_ulong,
}
#[link(name = "z")]
extern "C" {
    fn compress2(dest: *mut u8, dest_len: *mut c_ulong, source: *const u8, len: c_ulong, level: c_int) -> c_int;
    fn zlibVersion() -> *const c_char;
    fn inflateInit2_(stream: *mut ZStream, window: c_int, version: *const c_char, size: c_int) -> c_int;
    fn inflate(stream: *mut ZStream, flush: c_int) -> c_int;
    fn inflateEnd(stream: *mut ZStream) -> c_int;
}
fn invalid() -> io::Error { io::Error::new(io::ErrorKind::InvalidData, "invalid RPC deflate frame") }

fn encode<'a>(raw: &'a [u8], buffer: &'a mut [u8]) -> &'a [u8] {
    if raw.len() < MIN_COMPRESS { return raw; }
    let mut n = buffer.len() as c_ulong;
    // SAFETY: both slices are live and disjoint; zlib respects dest_len.
    // compress2 produces a zlib wrapper: 2-byte header and 4-byte Adler32.
    // The negotiated protocol transports its raw DEFLATE payload only.
    let status = unsafe { compress2(buffer.as_mut_ptr(), &mut n, raw.as_ptr(), raw.len() as c_ulong, 1) };
    let n = n as usize;
    if status == Z_OK && n >= 6 && n <= buffer.len() && n - 6 + 8 < raw.len() - raw.len()/8 {
        &buffer[2..n-4]
    } else { raw }
}
fn decode(wire: &[u8], raw: usize, buffer: &mut [u8]) -> io::Result<()> {
    if raw == 0 || raw > MAX || wire.is_empty() || wire.len() >= raw || buffer.len() <= raw { return Err(invalid()); }
    // SAFETY: z_stream permits zero initialization (null default allocators).
    // The slices remain valid and disjoint for the synchronous C calls.
    // inflateEnd runs once after successful init, including error paths.
    let mut stream: ZStream = unsafe { mem::zeroed() };
    stream.next_in = wire.as_ptr() as *mut u8;
    stream.avail_in = wire.len() as c_uint;
    stream.next_out = buffer.as_mut_ptr();
    stream.avail_out = (raw+1) as c_uint;
    unsafe {
        if inflateInit2_(&mut stream, -15, zlibVersion(), mem::size_of::<ZStream>() as c_int) != Z_OK { return Err(invalid()); }
        let result = inflate(&mut stream, Z_FINISH);
        let complete = result == Z_STREAM_END && stream.total_in as usize == wire.len() && stream.total_out as usize == raw;
        let end = inflateEnd(&mut stream);
        if !complete || end != Z_OK { return Err(invalid()); }
    }
    Ok(())
}
async fn write_frames<R: AsyncRead + Unpin, W: AsyncWrite + Unpin>(mut r: R, mut w: W) -> io::Result<()> {
    let mut raw = vec![0; MAX];
    let mut encoded = vec![0; MAX];
    loop {
        let n = r.read(&mut raw).await?;
        if n == 0 { return Ok(()); }
        let data = encode(&raw[..n], &mut encoded);
        let mut header = [0; 8];
        header[..4].copy_from_slice(&(n as u32).to_be_bytes());
        header[4..].copy_from_slice(&(data.len() as u32).to_be_bytes());
        w.write_all(&header).await?;
        w.write_all(data).await?;
        w.flush().await?;
    }
}
async fn read_frames<R: AsyncRead + Unpin, W: AsyncWrite + Unpin>(mut r: R, mut w: W) -> io::Result<()> {
    let mut encoded = vec![0; MAX];
    let mut raw = vec![0; MAX+1];
    loop {
        let mut header = [0;8];
        if r.read(&mut header[..1]).await? == 0 { return Ok(()); }
        r.read_exact(&mut header[1..]).await?;
        let n = u32::from_be_bytes(header[..4].try_into().unwrap()) as usize;
        let wire = u32::from_be_bytes(header[4..].try_into().unwrap()) as usize;
        if n == 0 || n > MAX || wire == 0 || wire > n { return Err(invalid()); }
        if n == wire {
            r.read_exact(&mut raw[..n]).await?;
        } else {
            r.read_exact(&mut encoded[..wire]).await?;
            decode(&encoded[..wire], n, &mut raw)?;
        }
        w.write_all(&raw[..n]).await?;
        w.flush().await?;
    }
}
pub async fn bridge<A: AsyncRead + AsyncWrite + Unpin, B: AsyncRead + AsyncWrite + Unpin>(remote: A, local: B) -> io::Result<()> {
    let (rr, rw) = tokio::io::split(remote);
    let (lr, lw) = tokio::io::split(local);
    tokio::select! { result = read_frames(rr, lw) => result, result = write_frames(lr, rw) => result }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn raw_deflate_roundtrip_and_strict_lengths() {
        let raw = vec![42;MAX];
        let mut encoded = vec![0;MAX];
        let wire = encode(&raw, &mut encoded);
        assert!(wire.len() < raw.len());
        let mut decoded = vec![0;MAX+1];
        decode(wire, MAX, &mut decoded).unwrap();
        assert_eq!(&decoded[..MAX], &raw);
        assert!(decode(wire, MAX-1, &mut decoded).is_err());
        let mut trailing = wire.to_vec();
        trailing.push(0);
        assert!(decode(&trailing, MAX, &mut decoded).is_err());
        assert_eq!(encode(b"control", &mut vec![0;MAX]), b"control");
    }
    #[tokio::test(flavor = "current_thread")]
    async fn framed_bridge_roundtrip() {
        let (mut host, remote) = tokio::io::duplex(8192);
        let (local, mut native) = tokio::io::duplex(8192);
        let task = tokio::spawn(bridge(remote, local));
        let payload = vec![7; MAX];
        let mut encoded = vec![0;MAX];
        let wire = encode(&payload, &mut encoded);
        host.write_u32(MAX as u32).await.unwrap();
        host.write_u32(wire.len() as u32).await.unwrap();
        host.write_all(wire).await.unwrap();
        let mut result = vec![0;MAX];
        native.read_exact(&mut result).await.unwrap();
        assert_eq!(result, payload);
        native.write_all(b"reply").await.unwrap();
        assert_eq!(host.read_u32().await.unwrap(), 5);
        assert_eq!(host.read_u32().await.unwrap(), 5);
        let mut out=[0;5];
        host.read_exact(&mut out).await.unwrap();
        assert_eq!(&out,b"reply");
        drop(host);
        task.await.unwrap().unwrap();
    }
}
