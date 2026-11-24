# Android guest resource budget

The first optimization stage retains the pinned GGML RPC wire protocol, native
CPU/Vulkan kernels, exact enrolled TLS certificate pin and isolated :rpc
process. Process isolation remains until the cooperative native stop/join path is
verified on real devices; a stuck Vulkan graph must not terminate unrelated UI work.

- Start two standby TLS relays and add slots on native claims, bounded by the
  existing eight-slot Host limit. Never cut an active or potentially claimed
  connection to reclaim memory.
- Stop waits are event-driven; eliminate 500 ms wakeups in heartbeat and
  reconnect backoff. Reuse the enrolled TLS context for heartbeat.
- Automatic Android reserve is bounded by device RAM and OS low-memory
  threshold plus 256 MiB; minimum 512 MiB. Existing saved explicit reserves
  remain unchanged. Host honors explicitly reported Android reserves above
  its 512 MiB minimum and retains 1 GiB for missing telemetry/non-Android.
  Existing 20% graph allowance and shared Vulkan/CPU accounting are unchanged.
- Actual process bitness determines the ARM32 address-space cap.
- A STOP intent for an inactive whole-model worker no longer initializes GGML in the UI process. ONNX probe sessions are released after validation, cached validation avoids loading weights on every lease, and switching model types releases the previous weights. Both model types expire after two idle minutes; external ONNX weights count toward admission.

## Hardware release gate

Compare same Host revision, GGUF hash, model layers, context, batch, device
mode, thermal state and Wi-Fi. Run at least five cold and warm trials with
screen on and off; include simultaneous native sessions and probe/upload.

Collect `adb shell dumpsys meminfo dev.nodexlm.app:rpc` (PSS, native heap),
`adb shell dumpsys meminfo dev.nodexlm.app`, `adb shell top -H -b -n 1`,
Host relay counters, free Vulkan heap, weight allocation, first-token latency,
decode throughput and LMKD/thermal events. Test ARM32 and ARM64, Android 10/11/12
and recent Android. No claimed percentage reduction until measured.

## Experimental Rust transport

PR debug APKs compile Rust JNI libraries for ARMv7 and ARM64. In Settings,
enable experimental Rust RPC and restart Guest. Kotlin remains the default;
stable/signed builds do not include the experimental module. The Rust
transport uses a current-thread Tokio executor with claim-driven slots,
bounded transfer buffers allocated only for claimed streams, exact certificate
pin and TLS handshake signature verification, HTTP/1.1 ALPN and existing
benchmark/claim markers. Shutdown cancels asynchronous sockets; draining
stops new connections without aborting active sessions. Android foreground
service, telemetry, safety policy and GGML kernels remain native/Kotlin.

For a local experimental build use `gradle -PnodexRustRpc=true :app:assembleDebug`
after installing Rust 1.94.0 and pinned Android SDK/NDK. The experimental lock
is generated locally; check in the reviewed Cargo.lock before release.
No production promotion until CI, TLS/protocol parity, hardware measurements
and signing/update compatibility pass.

## Remaining hardware-dependent engineering

The Rust relay now reuses at most two cleared 32 KiB buffers. Optional
CPU affinity uses measured CPU capacity within the Android cpuset; unsupported
or homogeneous devices retain default scheduling. Dedicated Guest mode closes
the UI task, leaving the foreground :rpc service alive (Android controls
whether the UI process remains cached). An Android-only pinned GGML patch now requests cooperative socket shutdown,
releases native backends on every server exit and joins the native thread with
a bounded timeout. The isolated process remains the fallback; do not unify
processes until real device tests prove shutdown even with in-flight graphs. Evaluate reduced ONNX packaging, OpenCL and KV/cache
policies only with parity and hardware measurements. Do not automatically lower the safety reserve under sustained
memory pressure. No automatic fallback from an authentication failure.
