# Mobile inference follow-up (PrivateLM evaluation)

This change is an independent implementation, not a port of third-party Flutter code.

## Implemented
- Android native backend selection: CPU-only no longer enumerates Vulkan modules in the RPC/embedded inference process; GPU inspection remains an explicit GPU load.
- Embedded inference reuses a successfully decoded sequence only if the next formatted prompt strictly extends it. Divergence, identical prompts, model reload, cancellation and decode failure invalidate the cache. No partial rewind is assumed for recurrent or sliding-window models.
- Prefix eligibility has a dependency-free C++ test: from the repository root run `c++ -std=c++17 -Wall -Wextra -Werror android/tests/prompt_prefix_test.cpp -o /tmp/nodexlm-prefix-test && /tmp/nodexlm-prefix-test`.

- CLI bulk import: `nodexlm hub import-directory --source /path/to/ggufs --dir ./models` copies validated complete model sets using the existing catalog. Add `--link` for explicit zero-copy hardlinks on the same filesystem; use only trusted immutable sources, because writes to either path change the same inode. The source is not deleted. Successful earlier models remain imported if a later model fails. This is a top-level scan, not recursive.
- CLI text document chat: `nodexlm chat --model model.gguf --prompt "Summarize" --document notes.md`. Local UTF-8 text only, up to 64 KiB; context limits still apply. The document is sent to the configured authenticated Host; no automatic external upload. DOCX extraction is described below.
- Go tests cover linked file identity, complete shards, no clobber, invalid headers and bounded text inputs. Full repository test execution remains pending.

- Experimental local vision CLI: `nodexlm local --model model.gguf --mmproj projector.gguf --image photo.png --prompt "Describe"`. The pinned llama.cpp backend handles vision; input is limited to JPEG/PNG, 4 MiB and 16 megapixels, projector must have CLIP GGUF architecture. Projector runs on Host CPU. No distributed vision; dashboard vision is described below.
- Explicit CLI external fallback: `nodexlm chat --model local.gguf --prompt "Hello" --fallback-url https://provider.example/v1 --fallback-model external-model`. Set `NODEXLM_FALLBACK_API_KEY` separately. The chosen provider receives the prompt/document on local HTTP 404/503 only; no fallback on authentication, timeout, targeted Guest or ambiguous failures. Do not use for private documents unless external processing is acceptable.
- QR-assisted enrollment: `nodexlm pairing --qr-host https://192.168.1.5:8443` with optional local qrencode executable. A compatible QR scanner opens the Android app and fills the pairing form; user confirmation and existing TLS/proof checks remain mandatory. No embedded camera scanner or dashboard QR yet.

- Experimental standalone diffusion: `nodexlm image --model model.safetensors --prompt "Landscape" --output image.png`. Requires a separately installed stable-diffusion.cpp sd-cli and self-contained compatible checkpoint. Bounded dimensions/steps, new PNG output only; no server, APK or distributed diffusion. Local process arguments may expose the prompt to same-machine process inspection.
- Android optional adaptive Wi-Fi performance lock uses the existing supervisor and authenticated claim count; releases after 30 seconds without claims. Defaults off, requires worker restart to apply. A long-lived claim can outlive generation; actual RTT/energy improvements are not established, and first-request latency may increase.
- CLI DOCX text extraction reads only word/document.xml (16 MiB archive, 2 MiB XML, 64 KiB output); does not execute macros, resolve external relationships or extract filesystem entries. Android document UI remains pending; the dashboard supports plain text.

- Android hardware advice now uses actual process bitness and the OS-aware Guest reserve; 32-bit processes remain capped even on 64-bit-capable phones. Chipset identification and advisory quantization guidance do not promise GPU compatibility or replace benchmarks.
- Dashboard chat accepts local UTF-8 text attachments up to 64 KiB; only the explicit message includes the document contents. Browser history does not retain raw attachment contents (model responses can still contain derived data).

- Dashboard local vision is opt-in with a separately selected CLIP GGUF projector. Host admission reserves twice projector weights plus 512 MiB as an estimate. Only a single inline JPEG/PNG (4 MiB, 16 MP) is accepted by chat completions; no remote URLs or file URLs. Web image contents are not saved to browser history. Distributed vision, Android vision and real model validation remain pending.

- LiteRT-LM standalone integration uses the official OpenAI-compatible registry server (https://developers.google.com/edge/litert-lm/cli/openai_server). Start `litert-lm serve --host 127.0.0.1 --port 9379` after importing your model. Then use `nodexlm local --engine litertlm --model registry-id --prompt "Hello"` or on an enrolled node `nodexlm node --litert --backend http://127.0.0.1:9379 --model registry-id`. Verifies the registry ID, rejects non-loopback URLs and redirects. No tensor RPC, managed dashboard lifecycle, bundled mobile SDK or GPU/NPU guarantees.

- Optional PDF text extraction via local Poppler `pdftotext`: 16 MiB input, 64 KiB text output, 20-second timeout, no OCR. The CLI accepts PDF files through `--document`.
- Admin dashboard can render a QR for the active temporary code using the Host's optional local `qrencode` executable. QR images are cleared on code renewal, expiry and logout; no third-party service.

- Optional native Android LiteRT-LM SDK build: `cd android && gradle -PnodexLiteRT=true :app:assembleDebug` (ARM64 only, Kotlin 2.2.21, AGP 8.10.1, SDK 0.16.1). Standard universal APK stays on existing dependencies and ABIs. The separate process offers foreground, one-shot local text generation from imported .litertlm models, CPU or explicit GPU. It is not a cluster worker, does not support tensor RPC, and needs optional-build and physical-device verification.
- Android processing screen now loads Vulkan for inspection only after explicit user action instead of entering the screen.

## Acceptance before enabling broader mobile changes
- Build both Android ABIs; CPU-only device must not initialize Vulkan in the RPC process; Auto/GPU/Hybrid must retain device discovery and fallback.
- Compare greedy output with cold cache for extended conversations, changed system prompts, identical requests, stop/cancel and model reload. Measure TTFT, PSS, tokens/s, thermal and energy on real devices. Prefix caching is scoped to the loaded embedded context; do not share it across unrelated trust domains.
- iOS guest: native Swift/ObjC++ or Rust authenticated outbound transport, exact pinned GGML RPC ABI, Metal memory reporting, foreground execution and explicit handling of OS suspension. No unauthenticated LAN RPC.
- Adaptive Wi-Fi: validate claim-based policy on Android and design RTT/jitter-driven tuning on mobile; avoid unnecessary probes during idle.
- Android host: reuse current coordinator and authenticated transport, not a parallel scheduler; admission must account for UI, unified memory and weights.
- LiteRT-LM: native Android SDK and managed dashboard lifecycle remain pending; verify accelerator discovery and do not claim GGML tensor splitting.
- Validate local Host vision against real models. Distributed vision, Android vision and diffusion image-generation API remain pending. Embedded Android camera integration remains pending. DOCX/PDF in the browser and Android/web directory import UI remain future work. External provider routing in the dashboard/API remains pending.
- Preserve existing measured RTT planner, Rust transport, parallel upload and cache rather than replacing them with PrivateLM's simpler placement.

These remaining items are not implemented by this change. Full Go, Android and native integration tests remain pending.
