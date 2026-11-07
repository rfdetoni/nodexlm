# Operations

## Management and API credentials

The Host serves HTTPS Guest traffic on `8443/tcp`, discovery on `46465/udp`, and HTTP management on loopback `8787/tcp`. Administrative CLI commands (`nodes`, `revoke`, `pairing`, `chat`, `bench`) now default to `http://127.0.0.1:8787`. Guest `join`/`guest` connections still use HTTPS on 8443. Replace old admin examples pointing at 8443 with a loopback URL or an SSH-forwarded port. `--web-addr off` disables this management/inference surface.

`NODEXLM_HOME` selects the configuration directory. Protect it with owner-only permissions. The Host creates distinct admin and enrollment tokens in pairing mode. `nodexlm web-token` prints the local admin token. A shell's `NODEXLM_ADMIN_TOKEN` overrides the stored token; ensure it matches the running process. Do not place secrets in command-line flags or commit them.

Set `NODEXLM_API_KEY` before startup to enable a separate inference-only credential (at least 32 characters, different from both admin and enrollment). It is restricted to `/v1/models` and `/v1/chat/completions`, including explicit standalone-worker targeting. This remains a loopback API; expose it only through an authenticated local tunnel. It supports text chat and the sampling fields listed in README.en.md, not the entire OpenAI API.

## Downloads

Both CLI and Web use the same safe HTTPS downloader. Model URLs and redirects are restricted to public destinations; proxy environment variables are deliberately ignored for these model transfers. Paired LAN traffic uses a separate pinned client.

Hidden `.nodexlm-*.part` and staging directories are retained after interruption. Retry the same source URL to resume. Resume requires a strong server ETag; weak/missing validators or a server ignoring Range cause a full restart. Complete-file SHA-256 covers both old and newly transferred bytes when provided. LFS digests on Hugging Face redirects are preserved. A checksum mismatch discards the invalid partial bytes.

Known Content-Length is checked against disk space. Unknown-length streams remain bounded by the transfer limit and filesystem errors. Shards have a combined 150 GiB limit. The first shard keeps its original basename so llama.cpp can locate its siblings. Never delete the partial files while a transfer is running. Use the Web interruption control to cancel first. To reclaim space, stop the Host and remove only partial files belonging to unwanted transfers.

## Diagnostics and metrics

`nodexlm doctor [--dir PATH] [--json]` reports local listeners, directory permissions, disk space, the certificate validity/SAN/fingerprint, AVX2 on Linux, glibc, Vulkan-tool availability and live cluster data when the default management listener is reachable. `unverified` explicitly means no conclusion was established. Local listener probes cannot prove a LAN firewall is open. Custom ports require separate reachability checks.

`GET /metrics` is served on loopback and requires the admin bearer token. It exposes current online nodes, connected RPC nodes and job counts by status. Completed/failed/cancelled jobs are retained for up to one hour, so those values are gauges rather than lifetime counters. This initial endpoint does not expose token counters, reconnect histories or latency histograms.

Every HTTP request receives a server-generated `X-Request-ID`. Correlation events are debug-level. `NODEXLM_LOG_FORMAT=json` selects JSON process logs; the default is human-readable slog output. Worker logs use `job_id`.

`nodexlm bench --node ID [--json]` uses marker 2 on an idle, authenticated TLS tunnel; marker 1 remains native GGML. The Guest must advertise support. Stop inference before running it. Results include RTT, total echoed bytes and aggregate bidirectional throughput, saved as `bench-ID.json`. No model tokens/s is inferred from these values.

The cluster-plan preview uses actual GGUF block metadata and complete shard size. Counts and memory are estimates; native GGML decides final remote allocation. It does not yet incorporate stored benchmark results, KV-cache size or measured per-model inference speed.

## Cancellation and scheduling

Disconnecting a Host SSE client cancels its native HTTP request. Unload cancels an active generation/load. Queued-worker requests are notified through channels; `priority` (-10..10, default 0) orders jobs stably within the bounded queue. `DELETE /v1/jobs/ID` cancels queued/running jobs. A Go worker checks its lease state every second, cancels its backend request and acknowledges cancellation before the node is reused. Legacy workers without that protocol may finish their current generation before their lease is reclaimed.

## Android

The RPC service uses a random loopback port per execution. This reduces predictable-port collisions; it does not authenticate GGML or isolate other local apps. Thermal status SEVERE always stops the service. Settings add charging-only operation, battery threshold and an hour window (including overnight windows). Restart the Guest after changing preferences. A safety stop posts its reason; restarting requires a deliberate user action. CPU/Vulkan and background behavior require physical-device validation.

## Docker

On Linux, `docker compose up --build -d` starts an unprivileged Host with a persistent `/data` volume and host networking for discovery. Management stays on real host loopback. Read-only root, dropped capabilities and `no-new-privileges` are configured. Retrieve the token using `docker compose exec host nodexlm web-token`. The packaged Linux x64 CPU RPC runtime is downloaded on first use. Other CPU/GPU variants may require an image with the matching runtime preinstalled or a source toolchain; GPU access is not enabled by this Compose file. Docker Desktop networking differs and is not validated here.

## Releases

CLI release archives carry GitHub artifact attestations and a CycloneDX inventory (`nodexlm.cdx.json`) of the CLI, Go standard library and shipped archives. Separately downloaded native runtimes are outside this CLI SBOM. Review their pinned revision/hashes independently.

Set `NODEXLM_VERIFY_PROVENANCE=1` for the POSIX installer or use `install.ps1 -VerifyProvenance` to require `gh attestation verify --repo rfdetoni/nodexlm` before executing the downloaded binary. GitHub CLI and any credentials it requires must already be configured. Old releases without attestations fail this mode. Checksums still protect transfer integrity when provenance verification is not requested. This is artifact provenance, not Authenticode or a third-party security audit.

The default remains experimental `edge` until a stable release exists; `NODEXLM_CHANNEL=stable` opts into GitHub's latest stable release. No stable version is invented by the installer.
