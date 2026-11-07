# NodexLM

A local inference cluster for Linux, macOS, Windows and Android Guests. The Host keeps the GGUF model and can place transformer layers on authenticated Guests through TLS-wrapped GGML RPC. One model is resident on the Host at a time. Standalone workers can serve their own models through the job queue.

This is experimental software for a trusted LAN/VPN. Loopback native RPC is unauthenticated: use dedicated machines/accounts and avoid untrusted local applications. Pairing binds an eight-character code to the Host certificate; it is not an externally audited PAKE.

## Quick start

Build with the Go version in `go.mod`:

```sh
go build -o nodexlm ./cmd/nodexlm
./nodexlm host
```

Open `http://localhost:8787`; retrieve the administrative token with `nodexlm web-token`. Pair a Linux Guest using `nodexlm guest --backend cpu` and the code shown by the Host. Android Guests use the native Android app. The public LAN listener on port 8443 serves Guest traffic; administrative commands use the loopback listener on port 8787. Remote administration requires an SSH tunnel.

Use the Web console or CLI to download/import GGUF files. For split GGUF, choose the original `-00001-of-0000N.gguf` filename; all parts must be present. CLI and Web downloads retain hidden partial files and safely resume using a server validator. SHA-256 is verified when supplied by Hugging Face; a missing digest is not presented as verified integrity.

```sh
nodexlm hub download --repo ggml-org/models --file tinyllamas/stories260K.gguf
nodexlm hub import --file /path/to/model-00001-of-00002.gguf
nodexlm doctor --json
nodexlm bench --node NODE_ID --json
```

`bench` measures an idle authenticated TLS tunnel, stores the result in the configuration directory, and requires an upgraded Guest. Its echo throughput is aggregate traffic in both directions, not inference tokens/s.

## OpenAI-compatible inference

Set a distinct `NODEXLM_API_KEY` of at least 32 characters before starting the Host. This credential only accesses `/v1/models` and `/v1/chat/completions`; it cannot change configuration or pair/revoke nodes. The admin token also works. The API remains loopback-only.

```sh
export NODEXLM_API_KEY='<your-inference-key-at-least-32-characters>'
nodexlm host
```

Configure clients with base URL `http://127.0.0.1:8787/v1`, the inference key, and the loaded model ID. Text messages, SSE streaming, `temperature`, `top_p`, `max_tokens` and string/array `stop` are supported. Tools, images and arbitrary OpenAI fields are not supported. Disconnecting cancels the managed generation. The Web chat keeps deletable browser-local history.

## Operations and validation

See [the operations guide](docs/OPERATIONS.md), [security policy](SECURITY.md), and [contributing guide](CONTRIBUTING.md). [Português](README.md).

```sh
go test -race ./... -timeout 120s
go vet ./...
node scripts/test-dashboard-models.cjs
sh scripts/test-install.sh
go build -o nodexlm ./cmd/nodexlm
python3 scripts/smoke.py
```

Real RPC inference tests require the matching native bundle and a tiny GGUF; see `scripts/e2e-rpc.py`. They are manually dispatched in Actions to avoid routine native build minutes. Linux CPU tests do not establish Android/Vulkan or other-platform compatibility.
