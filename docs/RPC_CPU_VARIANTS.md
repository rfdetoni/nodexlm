# Portable native CPU variants (r3 candidate)

The five-platform candidate pipeline builds GGML with shared backends, GGML_CPU_ALL_VARIANTS=ON, GGML_BACKEND_DL=ON and GGML_NATIVE=OFF. Upstream checks instruction availability at load time; x86 variants include baseline, AVX2, AVX-512, VNNI and (compiler permitting) AMX. ARM variants are selected by OS and capability. This is not a change to the active r2 bundle.

The job stages all shared modules alongside the binaries, rejects ambiguous filenames, verifies the parallel-loader marker, executes the staged server and worker, stores capability output, checks archive bounds, and exercises Linux CPU RPC through two Guests using the staged package. All five builds and attestations must succeed before the immutable candidate is published. No CPU-debug artifact can be promoted by this workflow. Release hashes must still be pinned by a reviewed manifest change; do not automatically promote r3.

Measure native system_info and controlled prefill/decode on real older x86, modern x86, Linux ARM64 and Apple Silicon. Validate Vulkan/Metal physically; runner device enumeration alone is not a GPU inference test.

The candidate patches GGML's dynamic loader to remove current-directory and GGML_BACKEND_PATH auto-loading, so unverified out-of-tree modules do not enter the signed/hash-pinned runtime through those discovery paths. Only the packaged sibling modules are discovered automatically.
