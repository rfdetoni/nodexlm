# Runtime registry

Run `nodexlm runtimes` or authenticated `GET /api/runtimes` to enumerate adapter candidates. Entries do not mean binaries are installed. Each external runtime must expose a local OpenAI-compatible /v1/models and /v1/chat/completions (SSE for streaming), and the configured model must be listed.

Host direct CLI (runtime already running on the same computer):

    nodexlm local --engine ollama --inference-endpoint http://127.0.0.1:11434 --model MODEL --prompt "Hello"

Guest, paired using the existing TLS workflow:

    nodexlm guest --engine ollama --inference-endpoint http://127.0.0.1:11434 --model MODEL

For other engines use their actual local origin and model ID. Set NODEXLM_RUNTIME_API_KEY locally if that runtime requires authentication. Never send it to the Host. The adapter rejects nonloopback origins, redirects and unlisted models. The worker checks model inventory during leases and advertises its engine in the device card. Choose the Guest in the chat Executor selector (text jobs only, no external fallback). A separate worker identity can also be paired on the Host machine; use a distinct --dir so it does not overwrite the Host configuration.

This supports separately installed OpenAI-compatible servers, including candidates for vLLM, SGLang, MLX-LM, LiteRT-LM, mistral.rs, ik_llama.cpp, llamafile, LM Studio, KoboldCpp, TabbyAPI, TGI, TensorRT-LLM and OpenVINO. No vendor binaries are installed by this adapter; exact endpoint and SSE support vary by release. Only stock llama.cpp supports the validated NodexLM tensor RPC cluster. Whole-model workers do not combine RAM across devices, and a node cannot switch native tensor ABI merely by changing an engine label. See TRELLIS.md for the isolated experimental Trellis runner.

For a locally installed Trellis candidate, the CLI auto-detects the format, and an already enrolled lazy worker can use `nodexlm node --engine trellis --model-path /absolute/model.gguf --model MODEL`. This is whole-model execution, not tensor RPC. The worker's CPU adapter is selected automatically; a compatible custom server can instead be attached using the registry API.
