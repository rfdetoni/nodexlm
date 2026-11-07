# GGUF models and Hugging Face

Compatibility recommendations use conservative reported memory and file size. They do not guarantee architecture, quantization, context, GPU driver or network performance.

```sh
nodexlm hub search --query 'qwen gguf' --json
nodexlm hub download --repo owner/repository --file quant/model.gguf
nodexlm hub import --file /path/model-00001-of-00002.gguf
```

For split GGUF, select the original first-shard filename. Every part must exist next to it with the original numbering. Model size and placement use the whole set; only architecture metadata needs to be read from its first part. The native server locates the remaining parts automatically.

CLI and Host downloads share resumable, bounded HTTPS transfers. Partial files remain hidden from model selection; retry the same source to resume with its strong validator. A changed/ignored Range response restarts safely. Files are never overwritten by concurrent publications. Checksum validation is only claimed when a real content digest is provided.

MoE options require GGUF expert metadata and a native binary supporting the corresponding flags. CPU experts, expert-layer offload and native cache settings change the allocation strategy but cannot make arbitrary models fit. Unsupported options fail explicitly.

Prefix KV reuse is owned by each resident native model and its RPC layer buffers. Full message history is still supplied; matching prefixes can reuse native state. Independent models never share incompatible KV tensors. Unloading releases the cache. No cross-model KV-sharing algorithm is claimed.
