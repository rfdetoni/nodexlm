# Android CPU kernels

The APK remains ARMv7 + ARM64 baseline compatible. The automatic CPU thread count samples only allowed CPUs using sched_getaffinity and reads cpu_capacity; unknown or homogeneous topology keeps the prior conservative rule. Manual thread limits remain authoritative. It does not pin inference workers.

## ARM64 dynamic variants & KleidiAI

On `arm64-v8a`, the build enables `GGML_BACKEND_DL=ON`, `GGML_CPU_ALL_VARIANTS=ON`, and `GGML_CPU_KLEIDIAI=ON` (`-PnodexKleidiAi=true`, default enabled; KleidiAI v1.32.0 archive checked against SHA-256 `a48d373e7c5d721a0aecbf5ddaf0bdf245bc8064e6b84086f13db0e102b553a6`). This ships runtime-dispatched CPU backend variants (`android_armv8.0_1` through `android_armv9.2_2`) plus `libggml-vulkan.so`, preserving `armv8-a` baseline compatibility while allowing modern ARM64 SoCs to select `dotprod`, `fp16`, `i8mm`, and `sve2` at runtime without raising the APK baseline. `armeabi-v7a` remains statically linked CPU-only without KleidiAI.

Standalone inference (`NativeEntry`) also uses `recommendedThreads`, Flash Attention (`AUTO`), `Q8_0` KV cache, and `512`-token prompt batches (`n_ubatch = n_batch`).

### Physical A/B benchmark (Xiaomi `2311DRK48I`, MediaTek Dimensity 8300-Ultra `MT6897`, Mali-G615 MC6, Android 16 API 36)

Measured with `llama-bench` (`llama.cpp` `940a30ee3`, `SmolLM2-360M-Instruct-Q4_K_M.gguf`, 256.35 MiB, `-p 128,512 -n 64 -r 3`):

| Configuration | Backend & Flags | Threads | `pp128` (t/s) | `pp512` (t/s) | `tg64` (t/s) |
|---|---|---:|---:|---:|---:|
| Baseline static `armv8.0` | CPU, `fa=0`, `f16` KV | 6 | 16.65 ± 0.04 | 23.95 ± 0.10 | 1.25 ± 0.07 |
| Baseline static `armv8.0` | CPU, `fa=0`, `f16` KV | 4 | 16.83 ± 0.08 | 23.77 ± 0.05 | 0.96 ± 0.03 |
| Static `armv8.0` + KleidiAI | CPU, `fa=0`, `f16` KV | 4 | 13.69 ± 0.07 | 19.51 ± 0.03 | 1.03 ± 0.01 |
| Vulkan GPU (`Mali-G615 MC6`) | Vulkan (`ngl=99`), `fa=1`, `q8_0` KV | 4 | 18.42 ± 0.10 | 21.51 ± 0.04 | 18.91 ± 0.76 |
| Dynamic `android_armv9.0_1` + KleidiAI | CPU (`DOTPROD+FP16+I8MM+SVE2`), `fa=0`, `f16` KV | 4 | 88.98 ± 1.93 | 77.84 ± 6.91 | 55.22 ± 3.10 |
| Dynamic `android_armv9.0_1` + KleidiAI | CPU (`DOTPROD+FP16+I8MM+SVE2`), `fa=1`, `q8_0` KV | 4 | 119.10 ± 18.44 | 103.02 ± 7.91 | 70.19 ± 1.77 |
| Dynamic `android_armv9.0_1` (6 mixed cores) | CPU, `fa=1`, `q8_0` KV | 6 | 103.16 ± 2.48 | 78.85 ± 2.23 | 38.23 ± 4.53 |

Older ARM64 devices without `dotprod`/`i8mm` and physical `armeabi-v7a` devices remain pending separate hardware measurements.

## Publication guard

PR debug artifacts use the isolated name `nodexlm-android-pr-test-only`. They are not update-compatible with the permanently signed application and are never public beta replacements. Main and tag builds require all four existing Android signing secrets and verify the keystore certificate against `android/signing-certificate.sha256` before SDK/NDK installation; the checked-in certificate hash remains authoritative.

## Política de threads do Guest (WP7)

Divergência reconfirmada: Android já aplica `NativeRpc.recommendedThreads` quando `rpc_threads=0`, tanto em ComputeService como RpcGuestService, e JNI usa sched_getaffinity somente para amostrar o cpuset. O campo existente foi esclarecido, sem mudar o padrão nem duplicar o seletor. Em desktop, `nodexlm guest --thread-policy performance` escolhe a contagem recomendada; `--threads N` é prioritário. Afinidade real de workers permanece follow-up: risco de herança por threads do runtime/driver não validado. O A/B acima (38,23 vs 70,19 t/s) motiva testes, não promete ganho no cluster.
