# Android OpenCL / Hexagon — feasibility at the pinned native revision

Checked against the NodexLM-pinned llama.cpp commit `940a30ee3b5593e1a66fe0aadc3b731989022552` (b12255). This is source-level feasibility, **not** an Android phone/RPC benchmark.

## OpenCL / Adreno

- The pinned upstream [OPENCL.md](https://github.com/ggml-org/llama.cpp/blob/940a30ee3b5593e1a66fe0aadc3b731989022552/docs/backend/OPENCL.md) documents Android support on specific recent Snapdragon/Adreno configurations, not on all GPUs or Android 10 devices. Driver and compiler versions matter; OpenCL headers and an ICD loader must be available for cross-compilation.
- NodexLM's universal APK currently builds ARMv7 CPU and ARM64 CPU/Vulkan. It explicitly disables dynamic GGML backends and links a statically bundled native runtime. There is **no validated OpenCL build or RPC device graph contract** for the current APK.
- Controlled experiment: use the *exact pinned* source, compile an opt-in ARM64 `GGML_OPENCL=ON` variant with the pinned NDK and OpenCL toolchain; run `test-backend-ops` on hardware; verify RPC `HELLO` / `DEVICE_COUNT`, model upload and graph execution end to end with a matching Host. Compare same GGUF/hash/quantization, context, batch, thermal and network conditions to CPU and Vulkan with `scripts/hardware-acceptance.py`.
- Do not automatically expose OpenCL, enable it for ARMv7, or assume Vulkan and OpenCL heaps are independent RAM. No universal Adreno acceleration claim is justified.

## Hexagon / NPU

- The pinned upstream [Snapdragon setup](https://github.com/ggml-org/llama.cpp/blob/940a30ee3b5593e1a66fe0aadc3b731989022552/docs/backend/snapdragon/README.md) uses Snapdragon-specific toolchain images with Hexagon SDK and specific HTP backend libraries. It does not provide a portable Android NPU API.
- The NodexLM universal APK intentionally does not ship the Qualcomm SDK or HTP backend binaries. Their deployment, compatible SoC/firmware, driver, ABI, licenses, and GGML RPC interoperability are **not established**.
- Controlled experiment: an opt-in Snapdragon-specific ARM64 build must first pass backend operations on a real device and then a full matching-version GGML RPC graph flow. An on-device `llama-cli` success alone does not demonstrate a distributed NodexLM Guest.
- Keep Hexagon disabled in the standard package until the toolchain, asset provenance, redistribution permissions, isolated failures and actual throughput are confirmed. Do not add UI switches that point to an unavailable backend.

## Acceptance required before enabling

Record device model, Snapdragon/Adreno or Hexagon hardware, Android API, ROM, driver, NDK, GGML commit, backend build options, model hash/quantization, thermal and battery state; verify Host + Android RPC load, prefill, decode, screen-off, restart and cancellation. Use several repeated comparable runs. If no compatible device is available, the result is **pendente externo**, not validated.

## RPC tensor cache finding (AND-08)

The pinned `ggml_backend_rpc_start_server(endpoint, cache_dir, ...)` API does expose a cache directory. However the pinned `ggml-rpc.cpp` writes tensor-hash files directly under that path; simply passing an app-private directory does **not** implement quota, eviction, corruption recovery or Host/runtime namespace separation. Android cache remains **disabled** pending a bounded native cache implementation and real cold/warm validation; enabling the raw upstream argument would risk filling device storage.
