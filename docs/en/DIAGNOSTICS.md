# Diagnostics

Start with `nodexlm doctor --json`. It reports local permissions, space, certificate identity/validity, listeners, Linux CPU/libc information and accessible cluster state. An unverified result is not a passed check. Local probes cannot establish firewall rules or connectivity from another device.

When pairing fails, check the active code window, the same trusted LAN, TCP 8443 and UDP 46465. Wi-Fi client isolation can block both UDP and mDNS. Provide an explicit `--server` if discovery fails. Never accept a different certificate merely to hide a pin mismatch.

When RPC is offline, inspect the Guest native process and backend version. A successful heartbeat only proves the REST path. A malformed response containing HTTP/2 binary bytes indicates CONNECT ALPN must remain HTTP/1.1. Idle tunnel failures, capacity rejections and reconnect counters appear in metrics and the observability panel.

Before benchmarking, stop inference and unload resident models for an inference workload. Network echo reports measured RTT/throughput; it is not a model speed prediction. The plan uses recent measurements where available and leaves unknown fields unknown. GPU allocation failures can require a smaller quantization/context or more available memory.

Set `NODEXLM_LOG_FORMAT=json` for structured process output. Request correlation uses `X-Request-ID`; worker events include `job_id`. Do not publish secrets or complete credential files when reporting a failure. Share the command, platform, revision, model metadata, redacted logs and exact failing step.

Use the repository's focused Go/JS/installer checks before a code change. Real native E2E and platform bundles have separate workflows. Go cross compilation does not prove native GPU, Android hardware, Docker networking or Windows/macOS runtime behavior.
