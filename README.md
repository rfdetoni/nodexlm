# NodexLM

Local AI inference and a lightweight coordinator/worker runtime for heterogeneous Linux, Windows, macOS and Android devices.

**Status: alpha.** The Go CLI/coordinator and local `llama-server` integration are functional; Android JNI service and Flutter desktop UI are experimental and have not yet been validated by device builds. Remote requests are scheduled as whole jobs on workers. A proof-of-concept of **one-model layer-distributed RPC inference** is now available for desktop Guests through a pinned and authenticated LAN tunnel; it has not been benchmarked against real multi-device GGUF workloads. Android as an RPC Guest and MoE expert-parallelism remain unimplemented.

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
# llama.cpp is provisioned automatically when a model is loaded.
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

## Single-model distributed inference (experimental)

The Host loads **one** GGUF. The Guests only run native ggml RPC compute
servers and do **not** download or load a second copy of the model. This is
the same layer-based RPC design used by PrivateLM, implemented independently.

**Host**, on a trusted LAN:

```sh
nodexlm host
# Open http://127.0.0.1:8787, authenticate with `nodexlm web-token`.
```

**Guest** on the same LAN:

```sh
nodexlm join --code <8-CHAR-CODE>
nodexlm rpc-node
```

`rpc-node` installs a pinned llama.cpp runtime automatically if needed. When
precompiled releases do not include RPC, NodexLM builds a matched server and
RPC worker from pinned upstream source (requires Git, CMake and C/C++ toolchain).
A native `ggml-rpc-server` listens only on `127.0.0.1:50052`, never on the
public LAN. Guest-initiated TLS connections, authenticated with the paired node
credential and Host certificate pin, carry the binary RPC streams. Each Host
relay also binds to loopback and gets passed to `llama-server --rpc`.

In the Host Web UI, select a model, check **Distribuir o modelo entre os Guests
RPC conectados**, save settings, and load it. NodexLM reads the GGUF
`<architecture>.block_count` without mapping its weights; it calculates how many
first layers to retain on the Host CPU from reported available RAM/VRAM and
assigns the remaining layers to the native RPC devices. The Host keeps
`output.weight` and `token_embd.weight` locally via a tensor override and
disables host operation offload. llama.cpp distributes the remote layers over
the reported RPC devices based on free device memory; it may fail to load if
a device is incompatible or has insufficient memory despite the estimate.
The GGUF remains on the Host; Guests receive tensor data and compute graphs,
not another persistent model copy. Host and Guest executables must use
compatible llama.cpp RPC protocol builds.

Do not confuse this with the separate `nodexlm node --model-path` workflow,
which handles whole requests using independent model copies on worker nodes.

**Limitations**: llama.cpp's RPC backend remains experimental. Network bandwidth,
CPU/GPU driver support, equal runtime versions and memory limits determine
whether a particular model can load and run. A wired LAN is recommended.
Active RPC tunnels consume sockets and a small amount of memory; when the
native worker exits, its process releases allocated model buffers. A Guest
without a valid memory report cannot be used for automatic layer placement.
Android still needs a dedicated native RPC worker implementation and live
heterogeneous benchmarks are pending. The model discovery
panel provides memory *estimates*, not verified compatibility guarantees.

## Android APK (GitHub Actions)

The [Android APK workflow](.github/workflows/android-apk.yml) runs **only on manual dispatch** or when pushing a `v*` tag, to minimize CI consumption. It installs Java 17, Gradle 8.11.1, Android SDK 35, NDK 27.2 and CMake, builds native `llama.cpp` from the pinned Git commit in `android/app/src/main/cpp/CMakeLists.txt`, checks that `libnodexlm.so` is packaged, and verifies APK signing.

**Manual debug APK:** open GitHub **Actions → Android APK → Run workflow** on `main`. Download the `nodexlm-android-debug` artifact when the run succeeds. This is an installable **debug-signed** ARM64 APK, not a production release. No signing secrets are required.

**Signed release APK:** configure these four repository **Actions secrets** before pushing a tag:

| Secret | Value |
| --- | --- |
| `ANDROID_KEYSTORE_BASE64` | Base64 of a *persistent* Android `.jks` keystore (one line) |
| `ANDROID_KEYSTORE_PASSWORD` | Keystore password |
| `ANDROID_KEY_ALIAS` | Alias of the release key |
| `ANDROID_KEY_PASSWORD` | Password of the release key |

Create and **back up** the keystore securely, outside the repository. Example (use a strong password and your own valid subject):

```sh
keytool -genkeypair -v -keystore nodexlm-release.jks -alias nodexlm \
  -keyalg RSA -keysize 3072 -validity 10000
base64 -w 0 nodexlm-release.jks  # Linux; paste the output in ANDROID_KEYSTORE_BASE64
```

Then push a version tag (`git tag v0.1.0 && git push origin v0.1.0`). The workflow requires **all four secrets** on tags, signs the ARM64 release APK, verifies the signature and embedded library, uploads the artifact and publishes it in **GitHub Releases as a prerelease**. The SHA-256 file accompanies the release. A partial set of secrets causes an explicit failure instead of silently producing an unsigned APK. Versions are derived from the Git tag and workflow run number.

If you need to trigger a build manually, avoid creating a release tag. Reuse the same signing keystore for upgrades; a newly generated keystore cannot update previously installed releases. Android/NDK compilation requires network access to fetch the pinned `llama.cpp` source. Native Android and actual device inference have not been validated yet, so a successful workflow build does not prove functional RPC inference on physical phones.

## Instalação Android direta (sem Play Store)

O NodexLM distribui **APK ARM64 assinado** diretamente pelo [GitHub Releases](https://github.com/rfdetoni/nodexlm/releases). Não requer Google Play, conta Google ou instalação pela loja.

- **Primeira instalação:** abra [Baixar a última versão estável](https://github.com/rfdetoni/nodexlm/releases/latest/download/NodexLM-android-arm64.apk) no celular, baixe o APK e confirme a instalação no Android (autorize a origem, se solicitado).
- **Atualizações:** o app verifica o `update.json` da última versão estável ao ser aberto (no máximo uma vez por dia), baixa um APK novo automaticamente apenas por rede não tarifada e confere seu SHA-256 e identificador de pacote. Também há botão **Verificar atualizações** para forçar busca/download na rede atual.
- **Instalação de atualização:** o NodexLM abre a tela **Instalar atualização** do Android. **Não existe instalação silenciosa** para apps comuns sem privilégios de sistema; o usuário precisa autorizar a origem e confirmar a instalação. O APK deve manter a **mesma chave de assinatura** da versão anterior.
- **Publicação:** tags `v1.2.3` criam releases estáveis e publicam `NodexLM-android-arm64.apk`, `SHA256SUMS.txt` e `update.json`; tags como `v1.2.3-rc.1` geram pré-lançamentos e não são oferecidas pelo atualizador automático. O fluxo exige os quatro secrets de assinatura Android existentes; sem eles não publica APK de produção.
- **CI mínimo:** execução manual gera um APK de testes assinado com a chave debug do runner e armazenado em Artifacts. Esses APKs debug não servem como atualização para uma instalação release. Nenhum build roda a cada push na `main`.

O aplicativo só baixa e prepara atualizações quando é aberto; o serviço de inferência em segundo plano não roda verificações periódicas. O processo Android e a compilação NDK precisam ser validados por uma execução real do workflow antes da distribuição ao público.
