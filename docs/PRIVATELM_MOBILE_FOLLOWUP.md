# Mobile inference follow-up (PrivateLM evaluation)

This change is an independent implementation, not a port of third-party Flutter code.

## Implemented
- Android native backend selection: CPU-only no longer enumerates Vulkan modules in the RPC/embedded inference process; GPU inspection remains an explicit GPU load.
- Embedded inference reuses a successfully decoded sequence only if the next formatted prompt strictly extends it. Divergence, identical prompts, model reload, cancellation and decode failure invalidate the cache. No partial rewind is assumed for recurrent or sliding-window models.
- Prefix eligibility has a dependency-free C++ test: from the repository root run `c++ -std=c++17 -Wall -Wextra -Werror android/tests/prompt_prefix_test.cpp -o /tmp/nodexlm-prefix-test && /tmp/nodexlm-prefix-test`.

## Acceptance before enabling broader mobile changes
- Build both Android ABIs; CPU-only device must not initialize Vulkan in the RPC process; Auto/GPU/Hybrid must retain device discovery and fallback.
- Compare greedy output with cold cache for extended conversations, changed system prompts, identical requests, stop/cancel and model reload. Measure TTFT, PSS, tokens/s, thermal and energy on real devices. Prefix caching is scoped to the loaded embedded context; do not share it across unrelated trust domains.
- iOS guest: native Swift/ObjC++ or Rust authenticated outbound transport, exact pinned GGML RPC ABI, Metal memory reporting, foreground execution and explicit handling of OS suspension. No unauthenticated LAN RPC.
- Adaptive Wi-Fi: use existing tunnel claims and measured RTT/jitter; only activate probes during active inference with bounded overhead and energy/thermal policy.
- Android host: reuse current coordinator and authenticated transport, not a parallel scheduler; admission must account for UI, unified memory and weights.
- LiteRT-LM: optional independent executor with verified artifact format, supported accelerator discovery and no claim of GGML tensor splitting.
- Multimodal and diffusion: optional executors, bounded media decoding and validated model capabilities; separate image-generation API. QR enrollment must preserve TLS pin and short-lived codes. Model directory import must validate format and avoid copying weights; documents need bounded extraction. External APIs require explicit opt-in and credentials.
- Preserve existing measured RTT planner, Rust transport, parallel upload and cache rather than replacing them with PrivateLM's simpler placement.

These remaining items are not implemented by this change.
