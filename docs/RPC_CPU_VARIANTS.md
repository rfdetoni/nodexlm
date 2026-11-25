# Portable native CPU variants (nodexlm-rpc-b12255-r3)

The six-platform RPC pipeline (`nodexlm-rpc-b12255-r3`, active in `internal/llamaruntime/revision.json`) builds GGML with shared backends, `GGML_CPU_ALL_VARIANTS=ON`, `GGML_BACKEND_DL=ON` and `GGML_NATIVE=OFF`. Upstream selects the best CPU variant at load time (`SSE4.2`, `AVX`, `AVX2`, `AVX-512`, `VNNI`, `AMX` on x86_64; runtime capability variants plus KleidiAI on Linux ARM64; Metal on macOS ARM64), alongside verified Vulkan RPC bundles on both `linux/amd64` and `windows/amd64`.

Each CI build stages all shared modules alongside the binaries, rejects ambiguous filenames, verifies the parallel-loader marker, executes the staged server and worker, stores capability output, checks archive bounds, and exercises Linux CPU RPC through two Guests using the staged package. All six builds and attestations must pass before publication, and release hashes are pinned in `internal/llamaruntime/revision.json`.

Measure native `system_info` and controlled prefill/decode on real older x86, modern x86, Linux ARM64, Windows GPU/CPU, and Apple Silicon. Validate Vulkan/Metal physically; runner device enumeration alone is not a physical GPU inference benchmark.

The runtime patches GGML's dynamic loader to remove current-directory and `GGML_BACKEND_PATH` auto-loading, so unverified out-of-tree modules do not enter the signed/hash-pinned runtime through those discovery paths. Only the packaged sibling modules are discovered automatically.
