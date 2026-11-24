# Android CPU kernels

The APK remains ARMv7 + ARM64 baseline compatible. The automatic CPU thread count samples only allowed CPUs using sched_getaffinity and reads cpu_capacity; unknown or homogeneous topology keeps the prior conservative rule. Manual thread limits remain authoritative. It does not pin inference workers.

ARM64 candidate builds enable GGML_CPU_KLEIDIAI through -PnodexKleidiAi=true. Upstream runtime capability detection selects dotprod/i8mm kernels for supported Q4/Q8 operations; no global architecture uplift. The exact v1.32.0 source archive is checked against SHA-256 a48d373e7c5d721a0aecbf5ddaf0bdf245bc8064e6b84086f13db0e102b553a6. ARMv7 never enables KleidiAI. PR debug APKs exercise the native build; signed beta and stable retain the previous kernels until device tests confirm parity, PSS, thermal behavior and throughput. Test at least an older ARM64 without dotprod, dotprod-only, i8mm and ARMv7. Do not report speed gains without the same GGUF and controlled A/B.
