# Host and Web console

Build with the version in `go.mod`, or install a verified release using `install.sh`/`install.ps1`. Start `nodexlm host`; configuration defaults to `~/.nodexlm` and can be relocated with `NODEXLM_HOME` or `--dir`.

The Host displays a temporary eight-character pairing code and LAN HTTPS addresses. Open `http://localhost:8787` and retrieve the admin credential with `nodexlm web-token`. Pair Guests using their own node credentials; never enter the admin token on a Guest.

Download or import a complete GGUF. Save runtime settings, review the estimated layer/KV plan, and load the model. The console polls load state while runtime installation happens. Distributed mode requires authenticated, compatible RPC Guests, not merely successful enrollment.

The primary model can use RPC. Up to three additional resident models can run on the Host when memory permits; they own separate native processes and KV state. The pinned RPC server handles one native client at a time, so additional models do not compete for its active connection. Select the target resident model in chat or pass its ID to the OpenAI endpoint.

Text generation supports SSE, cancellation, sampling controls and prefix-cache reuse. Histories remain in this browser until erased. Use the resource controls to unload individual models before changing configuration or benchmarking.

Administrative commands use loopback port 8787. Guests use TLS port 8443 and discovery UDP 46465. DNS-SD advertises `_nodexlm._tcp.local` and `nodexlm.local` on supported private IPv4 networks; UDP discovery remains the fallback. Multiple Hosts cannot safely share the same mDNS alias; explicit addresses still work.

Use `--rpc-transport unix` only with patched native binaries on POSIX. Automatic mode selects private Unix relays for the patched bundle; Windows retains loopback TCP. Remote administration requires an SSH tunnel. See [operations](../OPERATIONS.md) and [security](../../SECURITY.md).
