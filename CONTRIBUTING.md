# Contributing

Keep changes small enough to validate against a concrete failure or user workflow. Use the Go version in `go.mod`. The Go CLI intentionally has no third-party module dependencies.

Before a PR, run:

```sh
gofmt -w cmd internal
go test -race ./... -timeout 120s
go vet ./...
node --check internal/dashboard/static/app.js
node scripts/test-dashboard-models.cjs
sh scripts/test-install.sh
go build -o nodexlm ./cmd/nodexlm
python3 scripts/smoke.py
```

Add regression tests for concrete failures, especially TLS pinning, revoked credentials, truncated downloads, cancellation and process shutdown. Do not replace real integration checks with isolated mocks. Keep CI scoped by paths; native end-to-end tests are available through workflow dispatch.

The canonical native revision is `internal/llamaruntime/revision.json`, consumed by Go, Android Gradle/CMake and RPC builds. When updating it, review all upstream artifact hashes and build/publish new immutable RPC bundles before adding their hashes to `published`. Never reuse a mutable download manifest as the sole trust root. Existing pinned releases must remain immutable.

Android changes require an Android 10+ build plus device checks for CPU/Vulkan, thermal shutdown, battery policies and background execution. Desktop Flutter changes require Flutter analysis and platform testing. Neither is proven by a Go cross build.

Preserve the console behavior described in [DESIGN.md](DESIGN.md) and [UX-CONTRACT.md](UX-CONTRACT.md). Do not log prompts, bearer credentials or pairing codes in request telemetry. Report vulnerabilities through [SECURITY.md](SECURITY.md).
