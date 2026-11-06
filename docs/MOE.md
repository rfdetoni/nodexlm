# Mixture of Experts (MoE) — implementation and limits

## Implemented

- Detect GGUF model metadata without loading tensors into resident Go memory.
- Report architecture, total experts, experts used per token, and MoE flag in `nodexlm models --dir`.
- Pass through llama.cpp `--cpu-moe`, `--n-cpu-moe` and `--moe-cache-mib` for managed `llama-server` instances.
- Validate incompatible flag combinations and prevent setting MoE-only options for files without MoE GGUF metadata.
- Keep existing lazy model startup and idle unload behavior.

## Caveats

- MoE compute uses only selected experts, but all relevant weights still require storage; storage, RAM, VRAM, KV cache, batch sizes and load patterns influence peak resource usage.
- Some architectures or quantizations may not be supported by a given `llama.cpp` build.
- The GPU expert cache requires a recent llama.cpp binary supporting `--moe-cache-mib`.
- Android JNI uses llama.cpp's normal model loader and can open compatible MoE models, subject to actual memory requirements. This has not been validated on a physical Android device. CPU/GPU expert placement controls are not yet implemented in the Android UI.
- Cluster scheduling currently sends entire inference requests to one model-running node; it does not split MoE experts across devices.
- llama.cpp RPC can distribute tensors across remote compute devices, but **RPC tensor distribution is not automatically expert parallelism**. Deploying mixed hardware requires matching RPC backends and performance testing. RPC traffic must stay on a secured private network.
- Full model inference, cross-platform device builds, and token/s profiling remain to be validated.

## Next iterations

1. Benchmark tokens/s, first-token latency, peak RSS, VRAM, and energy for selected MoE models against dense baselines.
2. Profile CPU expert offload vs. GPU expert cache; automate safe placement recommendations only when hardware evidence supports them.
3. Add Android model metadata display and configurable expert placement, behind tested device-specific limits.
4. Evaluate secured llama.cpp RPC and per-expert scheduling independently. Avoid promising distributed expert parallelism until tested end-to-end.

References: [llama.cpp server flags](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md), [GGUF metadata](https://github.com/ggml-org/ggml/blob/master/docs/gguf.md), [RPC notes](https://github.com/ggml-org/llama.cpp/blob/master/tools/rpc/README.md).
