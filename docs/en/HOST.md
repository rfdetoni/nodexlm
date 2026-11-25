# Host and Web console

Build with the version in `go.mod`, or install a verified release using `install.sh`/`install.ps1`. Start `nodexlm host`; configuration defaults to `~/.nodexlm` and can be relocated with `NODEXLM_HOME` or `--dir`.

The Host displays a temporary eight-character pairing code and LAN HTTPS addresses. Open `http://localhost:8787` and retrieve the admin credential with `nodexlm web-token`. Pair Guests using their own node credentials; never enter the admin token on a Guest.

Download or import a complete GGUF. Save runtime settings, review the estimated layer/KV plan, and load the model. The console polls load state while runtime installation happens. Distributed mode requires authenticated, compatible RPC Guests, not merely successful enrollment.

The primary model can use RPC. Up to three additional resident models can run on the Host when memory permits; they own separate native processes and KV state. The pinned RPC server handles one native client at a time, so additional models do not compete for its active connection. Select the target resident model in chat or pass its ID to the OpenAI endpoint.

Text generation supports SSE, cancellation, sampling controls and prefix-cache reuse. Histories remain in this browser until erased. Use the resource controls to unload individual models before changing configuration or benchmarking.

Administrative commands use loopback port 8787. Guests use TLS port 8443 and discovery UDP 46465. DNS-SD advertises `_nodexlm._tcp.local` and `nodexlm.local` on supported private IPv4 networks; UDP discovery remains the fallback. Multiple Hosts cannot safely share the same mDNS alias; explicit addresses still work.

Use `--rpc-transport unix` only with patched native binaries on POSIX. Automatic mode selects private Unix relays for the patched bundle; Windows retains loopback TCP. Remote administration requires an SSH tunnel. See [operations](../OPERATIONS.md) and [security](../../SECURITY.md).

Optional mixed-cluster planner experiment: set `NODEXLM_RPC_COST_PLACEMENT=1` on the Host to compare estimated serial decode costs in speed mode. It uses comparable fresh benchmarks and measured RTT, not predicted or measured cluster throughput. The default does not apply this RTT cost model. Capacity mode retains all Guests. Validate with physical A/B before claiming improvements.

For remote layers with measured RTT of at least 5 ms, the plan may suggest n-gram speculation without applying it. Explicit autotuning measures draft windows of 4, 8 and 16 tokens on the same workload; comparable topology/basis and at least 25% draft acceptance are required. Pinned ngram-simple uses `--spec-ngram-simple-size-m`; compatibility with RPC is checked by the real E2E, not assumed.

Optional `NODEXLM_AUTO_RPC_MICROBENCH=1` collects bounded native tensor transfer measurements when the model pool is idle. The console can also request device measurements explicitly. These are transfer rates, not tokens/s. A divergent live cluster decode rate is only a re-benchmark advisory.

Autotune uses measured slow/fast links to choose microbatch candidates; prefill recommendations require measured improvement without more than 10% decode regression. The plan may suggest q8_0 KV when estimated admission improves; no settings are changed. Verified native parallel upload also counts selected RPC devices, including hybrid single Guests.

Optional `NODEXLM_GGUF_LAYER_BYTES=1` uses known GGUF tensor descriptors to estimate nonuniform contiguous layer placement, with memory headroom and Host non-block weights. Unknown types/shards retain the uniform fallback. Native placement still requires validation.

Network measurements use a discarded warmup and three legacy echo pairs on separate authenticated tunnels, with median and spread. Idle watchers block until claim or disconnect; TLS resumption is scoped to the paired client and retains certificate verification.

Optional `NODEXLM_DECISION_ADAPTIVE=1` prioritizes idle decision workers and incorporates EWMA/p95 latency, existing thermal/battery penalties and in-flight jobs; admission rechecks under lock. Existing leases and failover remain unchanged.
