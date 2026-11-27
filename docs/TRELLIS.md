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
