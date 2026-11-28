# Experimental Trellis / Gyro runtime

The ordinary llama.cpp v1.1.0 RPC bundle remains unchanged. The dashboard detects
GGML tensor IDs 144–149 from descriptors (including shards) and selects a separate
Host-only adapter. The source candidate is MIT-licensed halo-box/strix-llama.cpp at
dc5521842f7991ceae73139f9d242d8f877e12d4. This revision contains TQ2_T, TQK6,
TQK7, TQP5, TQP6, TQP7 and the external --ple option. Standard TQ2_0 is type 35.

## Installation

On Linux with Git, CMake and a C++ compiler, run from the NodexLM checkout:

    bash scripts/install-trellis-runtime.sh cpu

For a Vulkan-capable Host with the relevant SDK and drivers, choose vulkan
instead. CUDA and Metal build options are provided as candidates; not all
kernel/backend combinations have been validated. The script pins source, builds
locally, checks --ple help, and installs into a distinct revision/backend path
under NODEXLM_HOME (default ~/.nodexlm). No stock bundle is modified. Advanced
operators can set NODEXLM_TRELLIS_BIN to an absolute compatible binary path.
Select the corresponding Host GPU backend in the dashboard, disable distribution,
use one slot and turn speculative decoding off. The ordinary API authentication
and loopback binding remain in effect.

For Qwen3.8-Flash-Next -noPLE variants, download the shared table once:

    hf download Delgon/Qwen3.8-Flash-Next-PLE-GGUF Qwen3.8-Flash-Next-PLE-Q8_0.gguf --local-dir ~/.nodexlm/models

Place it beside the model in a custom model directory. The adapter passes --ple
automatically and defaults to lazy lookup for the sidecar. Ensure adequate SSD
space and memory for the selected context. The native loader validates the
table's actual shapes and compatibility. The existing weights do not need
re-downloading. Model card:
https://huggingface.co/agentionai/Qwen3.8-Flash-Next-Gyro-abliterated-Huihui-GGUF

## Validation and limits

The candidate RPC header has patch version 3 and 102 operations; stock has
patch version 2 and 101 operations. The fork also adds tensor IDs the stock
workers cannot deserialize. Never mix the two, even though both advertise
major/minor 10.0. Distributed execution is rejected before the handshake.
Dedicated matched Guest binaries, capability negotiation and real quantized
tensor tests are required before enabling distributed Trellis. No Android,
Windows, macOS or GPU binary release is claimed by this adapter. The CPU
candidate workflow checks source compilation and the CLI flag, not actual
126B-model generation or hardware performance.

Automatic provisioning: selecting a supported Trellis model triggers a pinned native build if the separate runtime is absent, using the installed Git/CMake/C++ toolchain. No administrator privileges are requested. Known noPLE models automatically fetch the reviewed 54.4 GB table, verify its SHA-256 and publish atomically. A Trellis load uses effective Host-only settings, leaving normal GGUF preferences unchanged. The preview does not download or compile anything. Insufficient Host memory fails before provisioning; matching distributed Trellis workers are still required. The stock RPC probe now enforces the patch version as well as major/minor.

Existing PLE files, including manually copied tables, must also match the reviewed SHA-256 before automatic provisioning accepts them. Successful verification is cached by regular-file identity, size and modification time for the current process only. Restarts or modifications trigger another verification; symbolic links are rejected. This does not add distributed PLE or Trellis RPC support.

### Isolated desktop CPU RPC candidate

On Linux, `bash scripts/install-trellis-runtime.sh cpu rpc-candidate` builds the pinned Host and Guest binaries with RPC enabled into a separate `cpu-rpc-candidate` directory. Before publishing locally, it runs the fork's multi-server and unreachable-endpoint tests and NodexLM's byte-for-byte upload/readback of all six Trellis tensor types through two native RPC workers. The CI builds that candidate once, instead of separately compiling the same Host-only source. These tests cover native transport, not model inference quality, PLE distribution, Android or TLS mesh integration. The normal Host auto-loader does not select this candidate, and it must not be mixed with stock RPC workers.

The RPC candidate pipeline now checks Bash syntax before compiling. The runtime also rechecks the PLE dependency at the actual model load boundary, and cancellation during the SHA-256 pass preserves the staged download for retry.

The mesh now has an explicit Trellis HELLO probe for the pinned 10.0.3 ABI. Stock probes retain their exact pinned version and cannot accept a Trellis worker. This probe only validates framing, version and device count; the fork-specific tensor allocation and full inference integration are still separate admission gates.

### Experimental Host-managed CPU cluster

Set `NODEXLM_EXPERIMENTAL_TRELLIS_RPC=1` on a released Host to allow a selected Trellis GGUF to request the pinned CPU candidate from enrolled desktop Guests through their signed runtime policy. Guests stop their former worker, build/cache the reviewed fork and reconnect without changing pairing. The Host accepts only matching ABI 10.0.3 and uses loopback TCP relays for this fork. Stock Android/iOS are not included. The candidate uses a separate source-built cache, disables stock upload patches and verifies native F32 roundtrip before loading. Unloading or a failed load restores the stock runtime policy. This is experimental; native six-format transport tests are not evidence of real-model graph correctness, GPU support or physical-device performance.

The experimental candidate deliberately uses the Host's opt-in environment flag; ordinary releases continue to keep Trellis Host-only. CPU runtime provisioning is source-based and needs a compiler, CMake and Git on each participating desktop. Mobile and GPU-specific Trellis builds remain separate validation targets.

The Trellis preflight now verifies all six pinned quantized wire layouts on each selected Guest, alongside F32 and exact device count. The native CI also runs small matrix-vector graphs for every format against the local pinned CPU reference. Source-built candidate installations contain a SHA-256 manifest of their binaries and libraries; missing or changed files cause quarantine and rebuild, not execution. These local manifests detect corruption and accidental replacement, not a privileged attacker able to modify both files and manifest. The candidate remains experimental until real-model and physical-device validation.

### External PLE placement

The pinned fork's qwen4exp model uses Host-side row gathering (qwen4exp_ple_gather_sync and llm_graph_input_ple::set_input), then passes the gathered embeddings through ggml_backend_tensor_set. For distributed noPLE models, automatic loading now requests on-direct on supported non-Windows Hosts: the external table is opened by llama_ple_disk and skipped as a full resident tensor. On Windows the fork's direct reader is explicitly unsupported, so auto keeps the existing mmap lazy mode. The full external table is not copied to tensor RPC Guests; whole-model remote execution would still require its own local verified dependency. The exact model graph and memory behavior remain subject to real-model validation.
