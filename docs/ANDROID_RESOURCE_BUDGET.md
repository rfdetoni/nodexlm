# Android guest resource budget

The first optimization stage retains the pinned GGML RPC wire protocol, native
CPU/Vulkan kernels, exact enrolled TLS certificate pin and isolated :rpc
process. Removing process isolation without a native stop/join API would leak
native allocations or terminate unrelated UI work.

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

## Further work (not in this stage)

Implement a Rust async TLS transport with exact cert pin AND handshake
signature verification, HTTP/1.1 ALPN, bounded response parsing, benchmark
marker support and cancellation; test all byte paths against current Host
before replacing Kotlin. Add native GGML shutdown and join before any process
unification. Evaluate buffer pooling, affinity, reduced ONNX packaging and
KV/cache policies only with parity and hardware measurements. Do not
automatically lower the safety reserve under sustained memory pressure.
