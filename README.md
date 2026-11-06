# NodexLM

Local AI inference and a lightweight coordinator/worker runtime for heterogeneous Linux, Windows, macOS and Android devices.

**Status: alpha.** The Go CLI/coordinator and local `llama-server` integration are functional; Android JNI service and Flutter desktop UI are experimental and have not yet been validated by device builds. Remote requests are scheduled as whole jobs on workers. Distributing *one* model across nodes by RPC / expert-parallelism is not yet implemented.

This is an independent implementation. PrivateLM was a functional reference, but no application source code from PrivateLM has been copied. Model execution uses [llama.cpp](https://github.com/ggml-org/llama.cpp) as an external runtime / native dependency.

## Build (Go CLI)

```sh
go build -o nodexlm ./cmd/nodexlm
go test ./...
```

## Join a cluster with an 8-character code

**Host:**

```sh
nodexlm host
# NODEXLM PAIRING CODE: K7M2X8Q9  (example, random each time)
```

**Guest on the same LAN:**

```sh
nodexlm join
# Enter host pairing code: [enter the host's code]
nodexlm node --model model.gguf --model-path ./models/model.gguf
```

Guests can also join through the desktop **Cluster** panel or the native Android **Join local cluster securely** screen. The code expires after 10 minutes, is not a persistent credential, and can be renewed with `nodexlm pairing`. For revocation use `nodexlm revoke --id NODE_ID`.

The discovery protocol uses `46465/udp`; all requests use `8443/tcp` with a pinned HTTPS certificate. `nodexlm join --server https://HOST_IP:8443` works when UDP broadcast is blocked. Pairing is intentionally LAN-only; no public Internet exposure. See [Pairing, security model, Android and limitations](docs/PAIRING.md) for details.

## Local inference

Requires a compatible `llama-server` available in `PATH`, and a supported GGUF model. The CLI loads the model in a separate process on demand and releases it after a configurable idle interval when used as a worker.

```sh
./nodexlm local --model ./models/model.gguf --prompt 'Hello'
./nodexlm models --dir ./models
```

## MoE models

The CLI discovers MoE models from GGUF metadata (`<architecture>.expert_count` and `.expert_used_count`), without mapping model tensors. Compatible llama.cpp MoE architectures run directly; no special flag is needed for default placement.

To reduce VRAM use by storing expert weights in system RAM:

```sh
./nodexlm local --model ./models/moe.gguf --moe-cpu --prompt 'Hello'
```

For partial placement and an optional GPU expert cache:

```sh
./nodexlm local --model ./models/moe.gguf --moe-cpu-layers 24 --moe-cache-mib 256 --prompt 'Hello'
```

The same options work for `nodexlm node --model-path ...` and are forwarded to a compatible `llama-server`. `--moe-cpu` and `--moe-cpu-layers` cannot be combined. Expert weights kept on CPU can consume substantial RAM; fewer experts active per token **does not** imply lower total model memory usage. Benchmark actual hardware before choosing settings.

More details: [docs/MOE.md](docs/MOE.md).

## Security and deployment

The coordinator uses HTTPS, per-node credentials and certificate pinning for newly paired guests. Local 8-character code onboarding is an alpha implementation and is **not yet a vetted PAKE**. Use a trusted LAN. Raw llama.cpp RPC is not exposed on untrusted networks. Do not expose the coordinator or workers to the public Internet. Complete example commands: `nodexlm help`.

## License

[AGPL-3.0-only](LICENSE). Copyright (C) 2026 Rodrigo Felipe de Toni Grave. Each dependency retains its original license and notices.

## Lightweight Web management (headless Linux / Windows / macOS)

`nodexlm host` also starts the **embedded Web console** on `http://127.0.0.1:8787`.
The static UI, dashboard API and cluster coordinator run in **one Go process**;
no Electron, Flutter, Node.js, browser process or desktop session is required on the server.
When no model is loaded, `llama-server` is not running. Model weights and accelerator
memory are held in the separate native inference process only while loaded.

```sh
# Make sure a compatible llama-server executable is installed separately.
nodexlm host --model-dir "$HOME/.nodexlm/models" --llama-bin llama-server
# On the same host, read the private admin token for the login form:
nodexlm web-token
# Open in your browser: http://127.0.0.1:8787
```

The panel can **download Hugging Face GGUF models** (streaming into a temporary file
with no full-file buffering), list model metadata, persist context/CPU/GPU/MoE
settings, explicitly load/unload a model, run local chat, show node status,
revoke nodes and renew LAN pairing codes. An active local model is managed by
`llama-server`, which is required for native inference. Models are not automatically
published as cluster workers; use `nodexlm node` for distributed job participation.

**Remote management of a machine without a desktop:**

```sh
ssh -L 8787:127.0.0.1:8787 username@server
# Browse http://127.0.0.1:8787 on your own computer.
# Run `nodexlm web-token` over the SSH connection on the server.
```

The Web UI deliberately binds **only to localhost**, uses a separate HTTP
listener from the LAN's HTTPS worker API and requires the administrator bearer
credential for all data/actions. The browser keeps this credential in tab-scoped
`sessionStorage` (not a persistent cookie). Avoid exposing 8787 via port-forward
or reverse proxy without separate HTTPS and authentication; SSH tunneling is
preferred. Disable the console with `nodexlm host --web-addr off`. To change its
local port, use `--web-addr 127.0.0.1:8888`.

Web resource usage scales with the number of live requests, not with keeping
an application window open. Refreshing the page does not restart inference.
