use jni::{
    objects::{JObject, JString, JValue},
    sys::{jlong, jstring},
    JNIEnv,
};
use ring::digest::{Context, SHA256};
use std::{
    fs::{self, OpenOptions},
    io::Write,
    sync::atomic::{AtomicBool, AtomicU64, Ordering},
    time::{Duration, Instant},
};

const CHUNK: usize = 256 * 1024;
static ACTIVE: AtomicBool = AtomicBool::new(false);
static CANCEL: AtomicBool = AtomicBool::new(false);
static BYTES: AtomicU64 = AtomicU64::new(0);
static BPS: AtomicU64 = AtomicU64::new(0);

struct ActiveDownload;
impl Drop for ActiveDownload {
    fn drop(&mut self) {
        ACTIVE.store(false, Ordering::Release);
    }
}

fn read_string(env: &mut JNIEnv<'_>, value: &JString<'_>) -> Result<String, String> {
    env.get_string(value)
        .map(|s| s.into())
        .map_err(|e| format!("Invalid JNI string: {e}"))
}

// An immutable Hub URL is enforced before the Java TLS stack is asked to
// connect. The OS trust manager validates HTTPS, including CDN redirects.
fn pinned_hub_url(source: &str) -> bool {
    let Some(rest) = source.strip_prefix("https://huggingface.co/") else {
        return false;
    };
    if rest.contains(['?', '#', '@', '\\']) {
        return false;
    }
    let parts: Vec<&str> = rest.split('/').collect();
    parts.len() >= 5
        && parts[0].len() >= 1
        && parts[1].len() >= 1
        && parts[2] == "resolve"
        && parts[3].len() == 40
        && parts[3].bytes().all(|b| b.is_ascii_hexdigit())
        && parts[4..].iter().all(|p| !p.is_empty() && *p != "." && *p != "..")
}

fn download(env: &mut JNIEnv<'_>, source: &str, destination: &str, checksum: &str, size: i64) -> Result<(), String> {
    if !pinned_hub_url(source) || checksum.len() != 64
        || !checksum.bytes().all(|b| b.is_ascii_hexdigit())
        || size < 8 || size > 150_i64 << 30 {
        return Err("Invalid pinned GGUF source or checksum".into());
    }
    if ACTIVE.compare_exchange(false, true, Ordering::AcqRel, Ordering::Acquire).is_err() {
        return Err("Native download already active".into());
    }
    let _active = ActiveDownload;
    CANCEL.store(false, Ordering::Release);
    BYTES.store(0, Ordering::Release);
    BPS.store(0, Ordering::Release);
    let url_string = env.new_string(source).map_err(|e| e.to_string())?;
    let url_object = env.new_object(
        "java/net/URL",
        "(Ljava/lang/String;)V",
        &[JValue::Object(url_string.as_ref())],
    ).map_err(|e| e.to_string())?;
    let connection = env.call_method(
        &url_object, "openConnection", "()Ljava/net/URLConnection;", &[]
    ).and_then(|v| v.l()).map_err(|e| e.to_string())?;
    env.call_method(&connection, "setConnectTimeout", "(I)V", &[JValue::Int(10_000)])
        .map_err(|e| e.to_string())?;
    env.call_method(&connection, "setReadTimeout", "(I)V", &[JValue::Int(30_000)])
        .map_err(|e| e.to_string())?;
    let code = env.call_method(&connection, "getResponseCode", "()I", &[])
        .and_then(|v| v.i()).map_err(|e| e.to_string())?;
    if code != 200 {
        let _ = env.call_method(&connection, "disconnect", "()V", &[]);
        return Err(format!("Native download HTTP {code}"));
    }
    let declared = env.call_method(&connection, "getContentLengthLong", "()J", &[])
        .and_then(|v| v.j()).map_err(|e| e.to_string())?;
    if declared > 0 && declared != size {
        let _ = env.call_method(&connection, "disconnect", "()V", &[]);
        return Err("Native GGUF content length mismatch".into());
    }
    let stream = env.call_method(&connection, "getInputStream", "()Ljava/io/InputStream;", &[])
        .and_then(|v| v.l()).map_err(|e| e.to_string())?;
    let result = stream_file(env, &stream, destination, checksum, size as u64);
    let _ = env.call_method(&stream, "close", "()V", &[]);
    let _ = env.call_method(&connection, "disconnect", "()V", &[]);
    result
}

fn stream_file(env: &mut JNIEnv<'_>, input: &JObject<'_>, destination: &str, checksum: &str, size: u64) -> Result<(), String> {
    let mut output = OpenOptions::new().write(true).create_new(true).open(destination)
        .map_err(|e| format!("Cannot create native staging file: {e}"))?;
    let result = (|| {
        let array = env.new_byte_array(CHUNK as i32).map_err(|e| e.to_string())?;
        let mut signed = vec![0_i8; CHUNK];
        let mut bytes = vec![0_u8; CHUNK];
        let mut digest = Context::new(&SHA256);
        let started = Instant::now();
        let mut previous = started;
        let mut last_bytes = 0_u64;
        let mut received = 0_u64;
        loop {
            if CANCEL.load(Ordering::Acquire) {
                return Err("Native model download cancelled".to_string());
            }
            let length = env.call_method(
                input, "read", "([B)I", &[JValue::Object(array.as_ref())]
            ).and_then(|v| v.i()).map_err(|e| e.to_string())?;
            if length < 0 { break; }
            if length == 0 {
                return Err("Native download made no progress".into());
            }
            let n = length as usize;
            received += n as u64;
            if received > size {
                return Err("Native response exceeds expected model size".into());
            }
            env.get_byte_array_region(&array, 0, &mut signed[..n])
                .map_err(|e| e.to_string())?;
            for index in 0..n {
                bytes[index] = signed[index] as u8;
            }
            digest.update(&bytes[..n]);
            output.write_all(&bytes[..n]).map_err(|e| e.to_string())?;
            BYTES.store(received, Ordering::Release);
            let now = Instant::now();
            if now.duration_since(previous) >= Duration::from_secs(1) {
                let delta = now.duration_since(previous).as_secs_f64();
                BPS.store(((received - last_bytes) as f64 / delta) as u64, Ordering::Release);
                previous = now;
                last_bytes = received;
            }
        }
        if received != size {
            return Err("Incomplete native GGUF model download".into());
        }
        if !digest.finish().as_ref().iter().zip(checksum.as_bytes().chunks_exact(2))
            .all(|(actual, pair)| {
                u8::from_str_radix(std::str::from_utf8(pair).unwrap_or(""), 16).ok() == Some(*actual)
            }) {
            return Err("Native GGUF SHA-256 mismatch".into());
        }
        output.sync_all().map_err(|e| e.to_string())?;
        let elapsed = started.elapsed().as_secs_f64().max(0.001);
        BPS.store((received as f64 / elapsed) as u64, Ordering::Release);
        Ok(())
    })();
    drop(output);
    if result.is_err() { let _ = fs::remove_file(destination); }
    result
}

#[no_mangle]
pub extern "system" fn Java_dev_nodexlm_app_NativeRustDownload_fetch(
    mut env: JNIEnv<'_>, _: JObject<'_>, source: JString<'_>, destination: JString<'_>,
    checksum: JString<'_>, size: jlong
) -> jstring {
    let result = (|| {
        let source = read_string(&mut env, &source)?;
        let destination = read_string(&mut env, &destination)?;
        let checksum = read_string(&mut env, &checksum)?;
        download(&mut env, &source, &destination, &checksum, size)
    })();
    super::response(&mut env, result.err().as_deref().unwrap_or(""))
}
#[no_mangle]
pub extern "system" fn Java_dev_nodexlm_app_NativeRustDownload_cancel(_: JNIEnv<'_>, _: JObject<'_>) {
    CANCEL.store(true, Ordering::Release);
}
#[no_mangle]
pub extern "system" fn Java_dev_nodexlm_app_NativeRustDownload_transferred(_: JNIEnv<'_>, _: JObject<'_>) -> jlong {
    BYTES.load(Ordering::Acquire) as jlong
}
#[no_mangle]
pub extern "system" fn Java_dev_nodexlm_app_NativeRustDownload_bytesPerSecond(_: JNIEnv<'_>, _: JObject<'_>) -> jlong {
    BPS.load(Ordering::Acquire) as jlong
}

#[cfg(test)]
mod tests {
    use super::pinned_hub_url;
    #[test]
    fn only_pinned_hub_urls_are_allowed() {
        let rev = "a".repeat(40);
        assert!(pinned_hub_url(&format!("https://huggingface.co/o/r/resolve/{rev}/model.gguf")));
        assert!(!pinned_hub_url("https://huggingface.co/o/r/resolve/main/model.gguf"));
        assert!(!pinned_hub_url(&format!("http://huggingface.co/o/r/resolve/{rev}/model.gguf")));
        assert!(!pinned_hub_url(&format!("https://huggingface.co.evil/o/r/resolve/{rev}/model.gguf")));
    }
}
