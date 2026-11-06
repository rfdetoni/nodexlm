# NodexLM Model Hub — Android, Desktop, CLI and Host

All clients use public **Hugging Face Hub REST APIs** to locate single-file GGUF
weights; the Go `internal/catalog` package is the shared library for the CLI,
Host web dashboard and Flutter desktop controller. The Android application has
a minimal Kotlin Hub API adapter, and a paired Android node can also ask its
Host for capacity-aware results via `GET /v1/nodes/catalog?q=...` using its node
credential (never the Host admin token).

## CLI

```sh
nodexlm hub search --query "qwen" --json
nodexlm hub search --query "qwen" --server https://HOST:8443 --ca server.crt
nodexlm hub download --repo OWNER/REPOSITORY --file MODEL-Q4_K_M.gguf --dir ./models
nodexlm hub import --file /path/to/model.gguf --dir ./models
nodexlm models --dir ./models
```

`--server` includes **currently RPC-ready** Guests for estimated cluster fit.
An authenticated Host admin credential is required, obtainable from the local
Host via `nodexlm web-token` (or `NODEXLM_ADMIN_TOKEN`). Never put the admin
credential in command-line flags. The desktop interface passes it to its Go
CLI subprocess through the environment.

## Android 10+

The **Modelos** tab searches the Hub, shows file sizes, and flags estimated
single-node or distributed candidates. Downloads go through Android's native
`DownloadManager` to app-specific storage and are not loaded into process RAM.
The tab exposes byte progress, cancellation, import from Android's document
picker, local model selection and direct reuse by the native worker. Downloads
must complete and pass GGUF header and size checks before selection.

After pairing, Android obtains Host-managed cluster recommendations. When the
Host is unreachable, Android falls back to the public Hub API and a conservative
local RAM estimate. Android RPC compute participation is **not yet implemented**:
its installed app currently executes complete model inference jobs. Do not treat
aggregate capacity as a promise of successful execution. Model shards, gated
repos, projector files and models requiring authentication are excluded from
this lightweight first implementation.

## Desktop

In the **Models** rail tab, search and download Hugging Face GGUF models or
import a previously downloaded GGUF file path. The GUI launches `nodexlm hub`
so downloads, storage validation and file naming behave exactly like the CLI.
The `nodexlm` CLI must be available alongside the GUI or in the user's PATH.
The local default directory is `./models`, editable in the UI.

## Security and limits

- Only explicit searches/downloads execute; the daemon never silently downloads large weights.
- Downloaded weights are streamed to disk; existing destination files are not overwritten.
- Supported public Hub downloads may redirect to Hugging Face CDN/Xet servers.
- All memory estimates reserve headroom and remain advisory; model formats,
  buffer placement, KV cache, actual RPC readiness and throughput may differ.
- An Android device must have sufficient free storage and memory. On 32-bit
  Android, even moderately sized models may exceed address-space limits.
- Never install an arbitrary downloaded GGUF as executable code.
