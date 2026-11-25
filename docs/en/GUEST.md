# Linux and desktop RPC Guest

The Host owns model files. An RPC Guest receives assigned tensors and graph operations; it does not need a complete GGUF.

```sh
nodexlm guest --backend cpu
```

Enter the Host pairing code. If multicast/UDP is blocked, add `--server https://HOST_IP:8443`. Guest state is stored under `NODEXLM_HOME`; protect it as a credential store. A pinned certificate is independent of DHCP addresses. Changing the Host identity requires deliberate re-pairing.

`--backend` selects CPU, Vulkan, or the platform's supported automatic backend. Runtime artifacts must match the pinned revision in `internal/llamaruntime/revision.json`. A source fallback requires Git, CMake and a C++ compiler; published reviewed bundles avoid this requirement. GPU drivers remain a platform prerequisite.

With the patched native runtime on POSIX, `--rpc-addr auto` starts the server in a fresh private Unix socket directory. Otherwise it uses a random loopback TCP port. `--rpc-addr 127.0.0.1:0` explicitly requests legacy TCP. Never bind unauthenticated GGML RPC to the LAN. On shared machines, dedicate an account/device; loopback TCP does not authenticate local apps.

Check the Host console for RPC readiness. Heartbeat/enrollment alone do not establish a working native tunnel. `nodexlm bench --node NODE_ID` measures an idle TLS tunnel. `nodexlm bench --model SMALL_MODEL --node NODE_ID` measures a real Guest inference workload while Host resident models are unloaded.

`deploy/nodexlm-guest.service` runs under a dedicated account and retains pairing state. Packages install unit files but do not enable or start them automatically. Stop the service before deleting/replacing enrollment data.

Use `--rpc-device CPU` to force the native CPU device even in a GPU-capable bundle. This is useful on VMs/drivers missing required model operations. The macOS CI bundle test uses this CPU fallback because the paravirtual GPU does not support RMS_NORM; it does not establish physical Metal compatibility.

Use `nodexlm guest --thread-policy performance` to opt into the recommended fast-core thread count; default physical-core behavior and explicit `--threads N` are preserved. On homogeneous CPUs or without reliable topology telemetry, the Guest performance policy also uses physical cores (capped at 256). No worker affinity is changed.

Network echo uses one idle TLS tunnel per pair (one warmup and one to three measured pairs). Four idle tunnels are recommended; a single slot must reconnect after warmup. Each pair has a five-second limit within a fifteen-second overall budget. Sample count is reported; zero spread with one sample does not establish stability.


Experimental CUDA: `nodexlm guest --backend cuda` selects an initialized CUDA device, never silently falls back to CPU. Linux can compile the pinned matching pair with a compatible installed CUDA toolkit, CMake and compiler; first build may be expensive. On other systems use `--rpc-bin` with a matching native binary. For multiple GPUs, select explicitly with `--rpc-device` or `CUDA_VISIBLE_DEVICES`; automatic per-card placement and published CUDA bundles are not yet certified. Default backend selection is unchanged.
