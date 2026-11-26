// SPDX-License-Identifier: AGPL-3.0-only
//! Native Android ranged GGUF transfers. All sockets use Android's platform
//! HTTPS trust store through JNI. Ranges never bypass the pinned Hub preflight.
//! The journal is only a download optimization; SHA-256 always gates publish.
use super::{BPS, BYTES, CANCEL, CONNECTION_BYTES, CONNECTION_ERRORS, CONNECTION_SPEED};
use jni::{
    objects::{JObject, JString, JValue},
    JNIEnv, JavaVM,
};
use ring::digest::{Context, SHA256};
use std::{
    fs::{self, File, OpenOptions},
    io::{Read, Seek, SeekFrom, Write},
    os::unix::fs::FileExt,
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc, Mutex,
    },
    thread,
    time::{Duration, Instant},
};

const RANGE_BYTES: u64 = 8 * 1024 * 1024;
const MAX_THREADS: usize = 4;
const MIN_PARALLEL_BYTES: u64 = 32 * 1024 * 1024;
const MAX_JOURNAL_BYTES: u64 = 1024 * 1024;

#[derive(Debug)]
enum TransferError {
    Unsupported,
    Fatal(String),
}
impl From<std::io::Error> for TransferError {
    fn from(e: std::io::Error) -> Self {
        TransferError::Fatal(e.to_string())
    }
}

struct Journal {
    complete: Vec<bool>,
    handle: File,
}

fn expected_range(start: u64, end: u64, total: u64) -> String {
    format!("bytes {start}-{end}/{total}")
}
fn chunks(size: u64) -> usize {
    size.div_ceil(RANGE_BYTES) as usize
}
fn chunk_bounds(index: usize, size: u64) -> (u64, u64) {
    let start = index as u64 * RANGE_BYTES;
    (start, (start + RANGE_BYTES).min(size) - 1)
}
fn valid_journal(text: &str, identity: &str, count: usize) -> Option<Vec<bool>> {
    let mut lines = text.lines();
    if lines.next()? != identity {
        return None;
    }
    let mut done = vec![false; count];
    for line in lines {
        let index = line.parse::<usize>().ok()?;
        if index >= count || done[index] {
            return None;
        }
        done[index] = true;
    }
    Some(done)
}
fn open_journal(destination: &str, checksum: &str, size: u64) -> Result<(File, Journal, String), String> {
    let journal_path = format!("{destination}.ranges");
    let identity = format!("nodexlm-ranges-v1 {checksum} {size} {RANGE_BYTES}");
    let count = chunks(size);
    let original = fs::metadata(destination).ok().filter(|meta| meta.is_file() && meta.len() == size);
    let previous = fs::metadata(&journal_path)
        .ok()
        .filter(|meta| original.is_some() && meta.is_file() && meta.len() <= MAX_JOURNAL_BYTES)
        .and_then(|_| fs::read_to_string(&journal_path).ok())
        .and_then(|v| valid_journal(&v, &identity, count));
    let file = OpenOptions::new().read(true).write(true).create(true)
        .open(destination).map_err(|e| e.to_string())?;
    // A stale or mismatched checkpoint must never reuse its completed chunks.
    if previous.is_none() {
        file.set_len(0).map_err(|e| e.to_string())?;
    }
    file.set_len(size).map_err(|e| e.to_string())?;
    let marks = previous.unwrap_or_else(|| vec![false; count]);
    let mut handle = OpenOptions::new().write(true).create(true)
        .append(true).open(&journal_path).map_err(|e| e.to_string())?;
    if marks.iter().all(|x| !x) {
        handle.set_len(0).map_err(|e| e.to_string())?;
        handle.write_all(format!("{identity}\n").as_bytes()).map_err(|e| e.to_string())?;
        handle.sync_data().map_err(|e| e.to_string())?;
    }
    Ok((file, Journal { complete: marks, handle }, journal_path))
}
fn read_header(env: &mut JNIEnv<'_>, conn: &JObject<'_>, name: &str) -> Result<Option<String>, TransferError> {
    let key = env.new_string(name).map_err(|e| TransferError::Fatal(e.to_string()))?;
    let result = env.call_method(
        conn, "getHeaderField", "(Ljava/lang/String;)Ljava/lang/String;",
        &[JValue::Object(key.as_ref())],
    ).and_then(|v| v.l()).map_err(|e| TransferError::Fatal(e.to_string()))?;
    if result.is_null() {
        return Ok(None);
    }
    let value = JString::from(result);
    env.get_string(&value).map(|x| Some(x.into()))
        .map_err(|e| TransferError::Fatal(e.to_string()))
}
fn set_header(env: &mut JNIEnv<'_>, conn: &JObject<'_>, key: &str, value: &str) -> Result<(), TransferError> {
    let k = env.new_string(key).map_err(|e| TransferError::Fatal(e.to_string()))?;
    let v = env.new_string(value).map_err(|e| TransferError::Fatal(e.to_string()))?;
    env.call_method(conn, "setRequestProperty", "(Ljava/lang/String;Ljava/lang/String;)V",
        &[JValue::Object(k.as_ref()), JValue::Object(v.as_ref())])
        .map_err(|e| TransferError::Fatal(e.to_string()))?;
    Ok(())
}
fn fetch_chunk(
    env: &mut JNIEnv<'_>, source: &str, file: &File,
    start: u64, end: u64, total: u64, worker: usize,
    stopped: &AtomicBool,
) -> Result<(), TransferError> {
    let url = env.new_string(source).map_err(|e| TransferError::Fatal(e.to_string()))?;
    let address = env.new_object("java/net/URL", "(Ljava/lang/String;)V",
        &[JValue::Object(url.as_ref())]).map_err(|e| TransferError::Fatal(e.to_string()))?;
    let conn = env.call_method(&address, "openConnection", "()Ljava/net/URLConnection;", &[])
        .and_then(|v| v.l()).map_err(|e| TransferError::Fatal(e.to_string()))?;
    let result = (|| {
        env.call_method(&conn, "setConnectTimeout", "(I)V", &[JValue::Int(12_000)])
            .map_err(|e| TransferError::Fatal(e.to_string()))?;
        env.call_method(&conn, "setReadTimeout", "(I)V", &[JValue::Int(25_000)])
            .map_err(|e| TransferError::Fatal(e.to_string()))?;
        set_header(env, &conn, "Accept-Encoding", "identity")?;
        set_header(env, &conn, "Range", &format!("bytes={start}-{end}"))?;
        let code = env.call_method(&conn, "getResponseCode", "()I", &[])
            .and_then(|v| v.i()).map_err(|e| TransferError::Fatal(e.to_string()))?;
        if code == 200 || code == 416 {
            return Err(TransferError::Unsupported);
        }
        if code != 206 {
            return Err(TransferError::Fatal(format!("HTTP {code}")));
        }
        let declared = read_header(env, &conn, "Content-Range")?
            .ok_or(TransferError::Unsupported)?;
        if declared.trim() != expected_range(start, end, total) {
            return Err(TransferError::Fatal("mismatched Content-Range".into()));
        }
        let length = env.call_method(&conn, "getContentLengthLong", "()J", &[])
            .and_then(|v| v.j()).map_err(|e| TransferError::Fatal(e.to_string()))?;
        if length > 0 && length as u64 != end - start + 1 {
            return Err(TransferError::Fatal("mismatched range length".into()));
        }
        let input = env.call_method(&conn, "getInputStream", "()Ljava/io/InputStream;", &[])
            .and_then(|v| v.l()).map_err(|e| TransferError::Fatal(e.to_string()))?;
        let transferred = (|| {
            let data = env.new_byte_array(128 * 1024).map_err(|e| TransferError::Fatal(e.to_string()))?;
            let mut signed = vec![0i8; 128 * 1024];
            let mut buf = vec![0u8; 128 * 1024];
            let mut offset = start;
            let mut last_mark = Instant::now();
            let mut last_total = CONNECTION_BYTES[worker].load(Ordering::Acquire);
            while offset <= end {
                if CANCEL.load(Ordering::Acquire) || stopped.load(Ordering::Acquire) {
                    return Err(TransferError::Fatal("cancelled".into()));
                }
                let read = env.call_method(&input, "read", "([B)I",
                    &[JValue::Object(data.as_ref())]).and_then(|v| v.i())
                    .map_err(|e| TransferError::Fatal(e.to_string()))?;
                if read <= 0 {
                    return Err(TransferError::Fatal("truncated native range response".into()));
                }
                let n = read as usize;
                if n as u64 > end - offset + 1 {
                    return Err(TransferError::Fatal("range exceeded requested bounds".into()));
                }
                env.get_byte_array_region(&data, 0, &mut signed[..n])
                    .map_err(|e| TransferError::Fatal(e.to_string()))?;
                for k in 0..n { buf[k] = signed[k] as u8; }
                let mut written = 0;
                while written < n {
                    let count = file.write_at(&buf[written..n], offset + written as u64)?;
                    if count == 0 {
                        return Err(TransferError::Fatal("short file write".into()));
                    }
                    written += count;
                }
                offset += n as u64;
                BYTES.fetch_add(n as u64, Ordering::AcqRel);
                let total_worker = CONNECTION_BYTES[worker].fetch_add(n as u64, Ordering::AcqRel) + n as u64;
                let now = Instant::now();
                if now.duration_since(last_mark) >= Duration::from_secs(1) {
                    let interval = now.duration_since(last_mark).as_secs_f64();
                    CONNECTION_SPEED[worker].store(((total_worker - last_total) as f64 / interval) as u64, Ordering::Release);
                    let total_speed: u64 = CONNECTION_SPEED.iter().map(|x| x.load(Ordering::Acquire)).sum();
                    BPS.store(total_speed, Ordering::Release);
                    last_mark = now;
                    last_total = total_worker;
                }

            }
            Ok(())
        })();
        let _ = env.call_method(&input, "close", "()V", &[]);
        transferred
    })();
    // Clear a pending Java IOException before the JNI thread exits.
    if env.exception_check().unwrap_or(false) { let _ = env.exception_clear(); }
    let _ = env.call_method(&conn, "disconnect", "()V", &[]);
    if env.exception_check().unwrap_or(false) { let _ = env.exception_clear(); }
    result
}
fn verify_file(file: &mut File, checksum: &str, size: u64) -> Result<(), String> {
    file.seek(SeekFrom::Start(0)).map_err(|e| e.to_string())?;
    let mut digest = Context::new(&SHA256);
    let mut buffer = [0u8; 256 * 1024];
    let mut remaining = size;
    let mut magic = [0u8; 4];
    file.read_exact(&mut magic).map_err(|e| e.to_string())?;
    if &magic != b"GGUF" {
        return Err("GGUF magic mismatch".into());
    }
    digest.update(&magic);
    remaining -= 4;
    while remaining > 0 {
        if CANCEL.load(Ordering::Acquire) {
            return Err("Native model download cancelled".into());
        }
        let limit = buffer.len().min(remaining as usize);
        let n = file.read(&mut buffer[..limit]).map_err(|e| e.to_string())?;
        if n == 0 { return Err("truncated ranged model".into()); }
        digest.update(&buffer[..n]);
        remaining -= n as u64;
    }
    let result: String = digest.finish().as_ref().iter().map(|x| format!("{x:02x}")).collect();
    if result != checksum {
        return Err("GGUF SHA-256 mismatch".into());
    }
    file.sync_all().map_err(|e| e.to_string())
}
/// Ok(false) means the remote does not honor ranges; caller uses the
/// existing verified sequential implementation instead.
pub(super) fn try_download(
    env: &mut JNIEnv<'_>, source: &str, destination: &str, checksum: &str, size: u64,
) -> Result<bool, String> {
    if size < MIN_PARALLEL_BYTES { return Ok(false); }
    let vm = env.get_java_vm().map_err(|e| e.to_string())?;
    let (mut file, state, journal_path) = open_journal(destination, checksum, size)?;
    let total = chunks(size);
    let complete = Arc::new(Mutex::new(state));
    let available: Vec<usize> = {
        let lock = complete.lock().map_err(|e| e.to_string())?;
        (0..total).filter(|&idx| !lock.complete[idx]).collect()
    };
    let resumed = size - available.iter().map(|&idx| {
        let (start, end) = chunk_bounds(idx, size);
        end - start + 1
    }).sum::<u64>();
    BYTES.store(resumed, Ordering::Release);
    let next = std::sync::atomic::AtomicUsize::new(0);
    let stopped = AtomicBool::new(false);
    let started = Instant::now();
    let concurrency = if size < 128 * 1024 * 1024 { 2 } else { MAX_THREADS };
    let result: Result<(), TransferError> = thread::scope(|scope| {
        let mut handles = Vec::with_capacity(concurrency);
        for worker in 0..concurrency {
            let vm: &JavaVM = &vm;
            let target = &file;
            let journal = Arc::clone(&complete);
            let pending = &available;
            let cursor = &next;
            let stopped = &stopped;
            handles.push(scope.spawn(move || -> Result<(), TransferError> {
                while !stopped.load(Ordering::Acquire) && !CANCEL.load(Ordering::Acquire) {
                    let n = cursor.fetch_add(1, Ordering::AcqRel);
                    if n >= pending.len() { break; }
                    let id = pending[n];
                    let (start, end) = chunk_bounds(id, size);
                    let mut last_error = TransferError::Fatal("uninitialized range".into());
                    let mut ok = false;
                    for attempt in 0..3 {
                        if CANCEL.load(Ordering::Acquire) || stopped.load(Ordering::Acquire) {
                            break;
                        }
                        // A short-lived JNI attachment bounds local Java refs
                        // without retaining thousands of URLConnection objects.
                        let result = {
                            let mut attached = vm.attach_current_thread()
                                .map_err(|e| TransferError::Fatal(e.to_string()))?;
                            fetch_chunk(&mut attached, source, target, start, end, size, worker, stopped)
                        };
                        match result {
                            Ok(()) => { ok = true; break; }
                            Err(TransferError::Unsupported) => {
                                stopped.store(true, Ordering::Release);
                                return Err(TransferError::Unsupported);
                            }
                            Err(e) => {
                                CONNECTION_ERRORS[worker].fetch_add(1, Ordering::AcqRel);
                                last_error = e;
                                if attempt < 2 { thread::sleep(Duration::from_millis(200 * (attempt + 1))); }
                            }
                        }
                    }
                    if !ok {
                        stopped.store(true, Ordering::Release);
                        return Err(last_error);
                    }
                    // Flush actual range bytes BEFORE checkpointing it. A torn
                    // tail in the journal is never allowed to publish a model.
                    let mut guard = journal.lock().map_err(|e| TransferError::Fatal(e.to_string()))?;
                    target.sync_data()?;
                    guard.handle.write_all(format!("{id}\n").as_bytes())?;
                    guard.handle.sync_data()?;
                    guard.complete[id] = true;
                }
                Ok(())
            }));
        }
        let mut first = Ok(());
        for handle in handles {
            match handle.join() {
                Ok(Err(e)) if first.is_ok() => first = Err(e),
                Err(_) if first.is_ok() => first = Err(TransferError::Fatal("native download worker panicked".into())),
                _ => {}
            }
        }
        first
    });
    match result {
        Err(TransferError::Unsupported) => {
            drop(file);
            let _ = fs::remove_file(destination);
            let _ = fs::remove_file(journal_path);
            Ok(false)
        }
        Err(TransferError::Fatal(msg)) => Err(msg),
        Ok(()) => {
            if CANCEL.load(Ordering::Acquire) {
                return Err("Native model download cancelled".into());
            }
            // Even after a valid range journal, re-hash every byte. The journal
            // cannot be used as a source of integrity or model authenticity.
            verify_file(&mut file, checksum, size)?;
            drop(file);
            fs::remove_file(journal_path).map_err(|e| e.to_string())?;
            let transferred = CONNECTION_BYTES.iter().map(|n| n.load(Ordering::Acquire)).sum::<u64>();
            BPS.store((transferred as f64 / started.elapsed().as_secs_f64().max(0.001)) as u64, Ordering::Release);
            Ok(true)
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn partition_has_no_gaps_or_overlap() {
        for size in [1, 32 * 1024 * 1024, 33 * 1024 * 1024, 151 * 1024 * 1024] {
            let ranges: Vec<_> = (0..chunks(size)).map(|i| chunk_bounds(i, size)).collect();
            assert_eq!(ranges[0].0, 0);
            assert_eq!(ranges.last().unwrap().1, size - 1);
            for pair in ranges.windows(2) { assert_eq!(pair[0].1 + 1, pair[1].0); }
        }
    }
    #[test]
    fn reject_stale_and_corrupt_journal() {
        let identity = "nodexlm-ranges-v1 abc 33 8388608";
        assert_eq!(valid_journal(&format!("{identity}\n1\n3\n"), identity, 4), Some(vec![false,true,false,true]));
        assert_eq!(valid_journal("other\n1\n", identity, 4), None);
        assert_eq!(valid_journal(&format!("{identity}\n1\n1\n"), identity, 4), None);
        assert_eq!(valid_journal(&format!("{identity}\n5\n"), identity, 4), None);
        assert_eq!(expected_range(0, 8388607, 16777216), "bytes 0-8388607/16777216");
    }
}
