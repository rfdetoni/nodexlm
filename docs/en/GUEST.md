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
