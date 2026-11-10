# Operations

## Management and API credentials

The Host serves HTTPS Guest traffic on `8443/tcp`, discovery on `46465/udp`, and HTTP management on loopback `8787/tcp`. Administrative CLI commands (`nodes`, `revoke`, `pairing`, `chat`, `bench`) now default to `http://127.0.0.1:8787`. Guest `join`/`guest` connections still use HTTPS on 8443. Replace old admin examples pointing at 8443 with a loopback URL or an SSH-forwarded port. `--web-addr off` disables this management/inference surface.

`NODEXLM_HOME` selects the configuration directory. Protect it with owner-only permissions (`chmod 700 "$NODEXLM_HOME"` on Linux/macOS). If a pre-existing directory is group/world-accessible, startup now refuses to write credentials instead of silently changing an arbitrary directory's permissions; restrict that specific directory explicitly. Newly created NodexLM directories use mode 0700. The Host creates distinct admin and enrollment tokens in pairing mode. `nodexlm web-token` prints the local admin token. A shell's `NODEXLM_ADMIN_TOKEN` overrides the stored token; ensure it matches the running process. Do not place secrets in command-line flags or commit them.

Set `NODEXLM_API_KEY` before startup to enable a separate inference-only credential (at least 32 characters, different from both admin and enrollment). It is restricted to the OpenAI inference routes and response history created by the same credential. Explicit standalone-worker targeting remains text-only; Host-native inference can use RPC Guests for tools and agents. This remains a loopback API; expose it only through an authenticated local tunnel. See [OpenAI compatibility](OPENAI_COMPATIBILITY.md) for tool calls, Structured Outputs, Responses, embeddings, official SDK acceptance and explicit service/model limits.

## Downloads

Both CLI and Web use the same safe HTTPS downloader. Model URLs and redirects are restricted to public destinations; proxy environment variables are deliberately ignored for these model transfers. Paired LAN traffic uses a separate pinned client.

Hidden `.nodexlm-*.part` and staging directories are retained after interruption. Retry the same source URL to resume. Resume requires a strong server ETag; weak/missing validators or a server ignoring Range cause a full restart. Complete-file SHA-256 covers both old and newly transferred bytes when provided. LFS digests on Hugging Face redirects are preserved. A checksum mismatch discards the invalid partial bytes.

Known Content-Length is checked against disk space. Unknown-length streams remain bounded by the transfer limit and filesystem errors. Shards have a combined 150 GiB limit. The first shard keeps its original basename so llama.cpp can locate its siblings. Never delete the partial files while a transfer is running. Use the Web interruption control to cancel first. To reclaim space, stop the Host and remove only partial files belonging to unwanted transfers.

## Diagnostics and metrics

`nodexlm doctor [--dir PATH] [--json]` reports local listeners, directory permissions, disk space, the certificate validity/SAN/fingerprint, AVX2 on Linux, glibc, Vulkan-tool availability and live cluster data when the default management listener is reachable. `unverified` explicitly means no conclusion was established. Local listener probes cannot prove a LAN firewall is open. Custom ports require separate reachability checks.

`GET /metrics` is served on loopback and requires the admin bearer token. It exposes current online nodes, connected RPC nodes and job counts by status. Completed/failed/cancelled jobs are retained for up to one hour, so those values are gauges rather than lifetime counters. Host metrics include generation/error counters, cumulative duration histogram buckets, backend-reported completion tokens and recent per-model throughput/cache gauges. Tokens absent from native usage (including interrupted generations) are not guessed. `/api/observability` provides bounded per-node RTT histories, reconnect failures and tunnel capacity counters; the console plots actual RTT samples.

Every HTTP request receives a server-generated `X-Request-ID`. Correlation events are debug-level. `NODEXLM_LOG_FORMAT=json` selects JSON process logs; the default is human-readable slog output. Worker logs use `job_id`.

`nodexlm bench --node ID [--json]` uses marker 2 on an idle, authenticated TLS tunnel; marker 1 remains native GGML. The Guest must advertise support. Stop inference before running it. Results include RTT, total echoed bytes and aggregate bidirectional throughput, saved as `bench-ID.json`. No model tokens/s is inferred from these values.

The cluster-plan preview uses actual GGUF block metadata and complete shard size. Counts and memory are estimates; native GGML decides final remote allocation. It incorporates stored network samples (up to 24 hours), an F16 KV estimate when architecture metadata is available, and recent measured model speed. Network bottleneck thresholds are explicitly advisory; measurements can become stale after configuration changes and are not throughput predictions.

## Cancellation and scheduling

Disconnecting a Host SSE client cancels its native HTTP request. Unload cancels an active generation/load. Queued-worker requests are notified through channels; `priority` (-10..10, default 0) orders jobs stably within the bounded queue. `DELETE /v1/jobs/ID` cancels queued/running jobs. Go and upgraded Android standalone workers check lease state every second and cancel their backend/native generation. Legacy workers without that protocol may finish their current generation before their lease is reclaimed.

## Android

The patched Android RPC service uses a filesystem Unix socket within private app storage, connected through `LocalSocket`. Patched POSIX desktop bundles also support private Unix sockets; Windows/legacy runtimes use random loopback TCP and require a dedicated account/machine. Thermal status SEVERE always stops the service. Settings add charging-only operation, battery threshold and an hour window (including overnight windows). Restart the Guest after changing preferences. A safety stop posts its reason; restarting requires a deliberate user action. CPU/Vulkan and background behavior require physical-device validation.

## Docker

On Linux, `docker compose up --build -d` starts an unprivileged Host with a persistent `/data` volume and host networking for discovery. Management stays on real host loopback. Read-only root, dropped capabilities and `no-new-privileges` are configured. Retrieve the token using `docker compose exec host nodexlm web-token`. The packaged Linux x64 CPU RPC runtime is downloaded on first use. Other CPU/GPU variants may require an image with the matching runtime preinstalled or a source toolchain; GPU access is not enabled by this Compose file. Docker Desktop networking differs and is not validated here.

## Releases

CLI release archives carry GitHub artifact attestations and a CycloneDX inventory (`nodexlm.cdx.json`) of the CLI, Go standard library, declared Go modules and shipped archives/packages. Separately downloaded native runtimes are outside this CLI SBOM. Review their pinned revision/hashes independently.

Set `NODEXLM_VERIFY_PROVENANCE=1` for the POSIX installer or use `install.ps1 -VerifyProvenance` to require `gh attestation verify --repo rfdetoni/nodexlm` before executing the downloaded binary. GitHub CLI and any credentials it requires must already be configured. Old releases without attestations fail this mode. Checksums still protect transfer integrity when provenance verification is not requested. This is artifact provenance, not Authenticode or a third-party security audit.

The default remains experimental `edge` until a stable release exists; `NODEXLM_CHANNEL=stable` opts into GitHub's latest stable release. No stable version is invented by the installer.

## Resident models and packaging

A primary model may use RPC. Up to three additional models may run on the Host with conservative memory admission. Each native process owns its KV; no cross-model cache or automatic eviction interrupts another client. The pinned Guest server handles one native client at a time, so additional resident models never attach to its occupied backend. Unload all residents before `bench --model MODEL [--node ID]`.

Desktop workflows build `.deb` and `.rpm` for Linux amd64/arm64/armhf. Their dedicated system accounts are created at installation; services are not enabled or started automatically. Configure enrollment first, then enable the desired systemd unit. The CLI/Web archives remain available for seven OS/architecture targets.

Stable `vX.Y.Z` releases generate a Homebrew formula and winget manifests using the actual release digests. These are reviewable release artifacts; availability in the public Homebrew/winget indexes requires a separate accepted upstream submission. Do not claim that generating a manifest publishes it to those indexes.

DNS-SD advertises `nodexlm.local` and `_nodexlm._tcp.local` on private IPv4 multicast networks. UDP discovery remains available when multicast is blocked or another Host advertises the same alias. Discovery results never replace code/certificate verification.
