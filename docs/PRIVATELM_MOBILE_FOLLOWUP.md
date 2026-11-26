# Mobile inference follow-up (PrivateLM evaluation)

This change is an independent implementation, not a port of third-party Flutter code.

## Implemented
- Android native backend selection: CPU-only no longer enumerates Vulkan modules in the RPC/embedded inference process; GPU inspection remains an explicit GPU load.
- Embedded inference reuses a successfully decoded sequence only if the next formatted prompt strictly extends it. Divergence, identical prompts, model reload, cancellation and decode failure invalidate the cache. No partial rewind is assumed for recurrent or sliding-window models.
- Prefix eligibility has a dependency-free C++ test: from the repository root run `c++ -std=c++17 -Wall -Wextra -Werror android/tests/prompt_prefix_test.cpp -o /tmp/nodexlm-prefix-test && /tmp/nodexlm-prefix-test`.

- CLI bulk import: `nodexlm hub import-directory --source /path/to/ggufs --dir ./models` copies validated complete model sets using the existing catalog. Add `--link` for explicit zero-copy hardlinks on the same filesystem; use only trusted immutable sources, because writes to either path change the same inode. The source is not deleted. Successful earlier models remain imported if a later model fails. This is a top-level scan, not recursive.
- CLI text document chat: `nodexlm chat --model model.gguf --prompt "Summarize" --document notes.md`. Local UTF-8 text only, up to 64 KiB; context limits still apply. The document is sent to the configured authenticated Host; no automatic external upload. DOCX extraction is described below.
- Go tests cover linked file identity, complete shards, no clobber, invalid headers and bounded text inputs. Full repository test execution remains pending.

- Experimental local vision CLI: `nodexlm local --model model.gguf --mmproj projector.gguf --image photo.png --prompt "Describe"`. The pinned llama.cpp backend handles vision; input is limited to JPEG/PNG, 4 MiB and 16 megapixels, projector must have CLIP GGUF architecture. Projector runs on Host CPU. Dashboard vision supports the RPC placement described below.
- Explicit CLI external fallback: `nodexlm chat --model local.gguf --prompt "Hello" --fallback-url https://provider.example/v1 --fallback-model external-model`. Set `NODEXLM_FALLBACK_API_KEY` separately. The chosen provider receives the prompt/document on local HTTP 404/503 only; no fallback on authentication, timeout, targeted Guest or ambiguous failures. Do not use for private documents unless external processing is acceptable.
- QR-assisted enrollment: `nodexlm pairing --qr-host https://192.168.1.5:8443` with optional local qrencode executable. A compatible QR scanner opens the Android app and fills the pairing form; user confirmation and existing TLS/proof checks remain mandatory. The Android camera scanner and dashboard QR are described below.

- Experimental standalone diffusion: `nodexlm image --model model.safetensors --prompt "Landscape" --output image.png`. Requires a separately installed stable-diffusion.cpp sd-cli and self-contained compatible checkpoint. Bounded dimensions/steps, new PNG output only; no bundled APK diffusion runtime or distributed diffusion. Local process arguments may expose the prompt to same-machine process inspection.
- Android optional adaptive Wi-Fi performance lock uses the existing supervisor and authenticated claim count; releases after a measured 30–60 second grace without claims. Defaults off, requires worker restart to apply. A long-lived claim can outlive generation; actual RTT/energy improvements are not established, and first-request latency may increase.
- CLI DOCX text extraction reads only word/document.xml (16 MiB archive, 2 MiB XML, 64 KiB output); does not execute macros, resolve external relationships or extract filesystem entries. Shared parser also powers dashboard PDF/DOCX uploads.

- Android hardware advice now uses actual process bitness and the OS-aware Guest reserve; 32-bit processes remain capped even on 64-bit-capable phones. Chipset identification and advisory quantization guidance do not promise GPU compatibility or replace benchmarks.
- Dashboard chat accepts local UTF-8 text attachments up to 64 KiB; only the explicit message includes the document contents. Browser history does not retain raw attachment contents (model responses can still contain derived data).

- Dashboard local vision is opt-in with a separately selected CLIP GGUF projector. Host admission reserves twice projector weights plus 512 MiB as an estimate. Only a single inline JPEG/PNG (4 MiB, 16 MP) is accepted by chat completions; no remote URLs or file URLs. Web image contents are not saved to browser history. The language model can use RPC; the projector remains on the Host CPU.

- LiteRT-LM standalone integration uses the official OpenAI-compatible registry server (https://developers.google.com/edge/litert-lm/cli/openai_server). Start `litert-lm serve --host 127.0.0.1 --port 9379` after importing your model. Then use `nodexlm local --engine litertlm --model registry-id --prompt "Hello"` or on an enrolled node `nodexlm node --litert --backend http://127.0.0.1:9379 --model registry-id`. Verifies the registry ID, rejects non-loopback URLs and redirects. No tensor RPC, managed dashboard lifecycle, bundled mobile SDK or GPU/NPU guarantees.

- Optional PDF text extraction via local Poppler `pdftotext`: 16 MiB input, 64 KiB text output, 20-second timeout, no OCR. The CLI accepts PDF files through `--document`.
- Admin dashboard can render a QR for the active temporary code using the Host's optional local `qrencode` executable. QR images are cleared on code renewal, expiry and logout; no third-party service.

- Optional native Android LiteRT-LM SDK build: `cd android && gradle -PnodexLiteRT=true :app:assembleDebug` (ARM64 only, Kotlin 2.2.21, AGP 8.10.1, SDK 0.16.1). Standard universal APK stays on existing dependencies and ABIs. The separate process offers foreground, one-shot local text generation from imported .litertlm models, CPU or explicit GPU. It also supports a whole-model cluster worker; no tensor RPC.
- Android processing screen now loads Vulkan for inspection only after explicit user action instead of entering the screen.

- Android Storage Access Framework directory import now groups complete GGUF shard sets, rejects orphan shards and normalized filename collisions, skips existing valid sets, and retains the active model selection. Copies stream sequentially; no zero-copy promise for content-provider URIs. The directory scan is nonrecursive and capped at 1024 entries.

## Consolidated P0–P2 implementation

| Item | Implementation and constraints |
| --- | --- |
| P0 Android Host | Optional ARM64 foreground service runs the existing Go coordinator and pinned CPU llama-server. Local dashboard; authenticated remote Guests. Cross-process lease excludes concurrent local runtimes; ActivityManager reports available RAM. |
| P0 iOS/iPadOS Guest | SwiftUI, Keychain, pinned GGML Metal RPC, existing Go pairing/relay, foreground only; stops on background, low memory or serious thermal conditions. |
| P1 LiteRT-LM worker | Optional SDK whole-model worker reuses existing leases, cancellation and job completion. One complete response chunk rather than incremental tokens; no GGML tensor split. |
| P1 distributed vision | GGUF language layers may be placed by existing RPC planner; separate CLIP projector and its memory remain local to the Host. No remote projector or arbitrary image URLs. |
| P1 diffusion | Local sd-cli via /v1/images/generations and dashboard; one request, no loaded LLM, configured checkpoint and explicit limits. |
| P1 adaptive Wi-Fi | Existing authenticated heartbeat carries recent RPC RTT/spread. EWMA adjusts idle grace from 30 to 60 seconds. No additional active probes; claims retain lock. |
| P2 external fallback | Host environment supplies HTTPS URL, model and key. Dashboard requires explicit consent per message, only when local model unavailable. |
| P2 camera QR | ZXing camera scanner decodes local pairing URI; normal confirmation and TLS proof remain required. |
| P2 documents | Browser bounded uploads via shared parser; Android local GGUF/LiteRT chat supports UTF-8/DOCX and PDF text on API 35+. Older Android can use Host web console; no OCR. |
| Directory import | Android SAF and authenticated dashboard import; streaming copies or explicit Host hardlinks only. |

### Build and configure

- Standard universal APK remains free of the optional Host and LiteRT binaries.
- Experimental combined ARM64 APK: `cd android && gradle -PnodexHost=true -PnodexLiteRT=true :app:assembleDebug`. Requires the pinned NDK, Go, CMake/Ninja and network access to pinned sources. Host CPU backend only.
- iOS: on macOS with Xcode, Go and XcodeGen, run `bash scripts/build-ios-guest.sh`; configure Apple development signing in Xcode to install on a physical device. No simulator/unsigned-device execution guarantee.
- Configure `NODEXLM_SD_MODEL` with a local compatible checkpoint and optionally `NODEXLM_SD_BIN` with a trusted sd-cli executable. Unload language models first. The prompt is passed as a local process argument.
- Configure `NODEXLM_FALLBACK_URL`, `NODEXLM_FALLBACK_MODEL`, `NODEXLM_FALLBACK_API_KEY` on the Host. No automatic external upload unless the user opts in per request.
- Install Poppler for desktop PDF extraction. On Android, local PDF requires API 35 and text-layer PDFs.

## Acceptance gates (not implied by source code)

- Universal APK, optional ARM64 APK and unsigned iOS build; installation on real Android and signed Apple devices.
- Real mixed-device cluster: Android Host + Windows/Linux/macOS/Android/iOS Guests, authenticated enrollment, model placement, disconnect, reconnect, cancellation and no duplicate memory allocation.
- LiteRT CPU/GPU model load, job completion, idle unload and cancellation; distributed vision with known compatible LLM/projector; sd-cli generation; QR camera and bounded document extraction.
- Compare TTFT, decode throughput, PSS, thermal and power against previous build; prove Wi-Fi adaptation improves or at least does not regress measured performance.
- No physical benchmark or acceptance test is inferred from CI compilation. Keep experimental paths opt-in until tested.
