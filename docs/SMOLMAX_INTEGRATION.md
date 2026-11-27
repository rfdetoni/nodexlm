# Smolmaxxing — port nativo completo candidato v1.0.0

Origem MIT: https://github.com/hyperionaidev/smolmaxxing, commit
8d12bb17fd389c25f3c6167a5b73f93034d6324f. O patch completo
`experiments/smolmax/full-v1.patch` preserva os 36 arquivos e os 126 hunks do
upstream original, adaptados ao llama.cpp v1.0.0 fixado em
b1b770d90d94cad9e9ea5b993bdbc383a522fb28. Licenças acompanham o binário.

## Escopo

- Pager MoE, prefetch e streaming de pesos, cache de repacking, kernels
  ARM64 NEON, Q1_0/Q2_K/Q2_0/TQ, operações binárias, atenção e DeltaNet.
- Alterações de loader, grafos e arquiteturas, incluindo suporte experimental
  a modelos com head reduzido e ajustes MTP.
- Ferramentas de benchmark e perplexidade.
- As opções de simulação de roteamento, ternário Q2_0 e descarte de atenção
  ficam explicitamente opt-in; o perfil de pager não ativa simulação por
  acidente. Não há promessa de equivalência numérica para todas as opções.
- Vulkan está no código candidato, mas o build inicial verifica CPU ARM64;
  Android, outros alvos e integração RPC não são aceitos automaticamente.

O candidato não substitui o runtime RPC10 ativo (`nodexlm-rpc-v1.0.0-r6`, conforme `internal/llamaruntime/revision.json`) nem o port FP32 isolado. O
pager/streaming opera somente em um processo local com GPU layers 0 e sem
Guests RPC. O script recusa uma mudança de revisão do llama.cpp até revisão
do port. A aceitação física e numérica de todos os kernels e arquiteturas
continua necessária antes de promoção a produção.

## Compilar e executar

Em Linux ARM64:

```sh
bash scripts/build-smolmax-native.sh /opt/smolmax
```

O script aplica o patch inteiro ao commit fixado, compila llama-server,
llama-bench e llama-perplexity, e registra o SHA-256 do servidor. Configure
GPU layers=0 e distribuição desativada no Host.

```sh
NODEXLM_SMOLMAX_MODE=moe NODEXLM_SMOLMAX_EXPERT_POOL=32 \
  NODEXLM_SMOLMAX_PREFETCH_K=0 NODEXLM_SMOLMAX_READERS=2 \
  nodexlm host --llama-bin /opt/smolmax/llama-server
```

Ajuste o pool à memória e aos experts ativos do modelo. Para streaming,
escolha um orçamento explícito e mantenha espaço para KV/cache e buffers:

```sh
NODEXLM_SMOLMAX_MODE=stream NODEXLM_SMOLMAX_RESIDENT_MB=512 \
  NODEXLM_SMOLMAX_WINDOW_MB=128 NODEXLM_SMOLMAX_READERS=2 \
  nodexlm host --llama-bin /opt/smolmax/llama-server
```

O launcher gerenciado usa `--no-repack` nos perfis de paginação/streaming
quando não há cache de repacking explícito. Para uso nativo direto,
configure a mesma flag. O pager e streaming exigem páginas OS de 4 KiB,
um slot, sem speculative decoding e load mode padrão. Sem mapeamento ou
com todos os pesos repacked, a
existência de uma zona de streaming não é garantida. Use
`NODEXLM_SMOLMAX_VERIFY=1` para diagnóstico, não para benchmark de latência.
O cache opcional `NODEXLM_SMOLMAX_REPACK_CACHE` precisa ser um caminho
absoluto em diretório existente; não reutilize entre GGUFs, quantizações e
binários diferentes. `NODEXLM_SMOLMAX_THREADS=performance` escolhe a
contagem de threads do cluster de maior capacidade; não fixa afinidade.

## Aceitação e promoção

O CI `smolmax-full.yml` roda um único build ARM64 após o PR sair do draft,
verifica o patch completo, os três executáveis, a inferência determinística
contra o bundle CPU ativo publicado (`r6`; originalmente validado contra `r5`) e o streaming denso com zona não vazia,
checagem dos bytes e comparação da saída. O artefato é preservado somente
se os testes passarem. O teste pequeno não cobre todos os caminhos:
pager com MoE real, quantizações, MTP, Vulkan, Android, páginas de tamanho
diferente e medições físicas de RSS/PSS, TTFT, throughput e energia ainda
precisam de aceitação específica. Não ativar o candidato globalmente antes
dessas verificações. O registro local do binário não constitui atestado
de desempenho ou de segurança.

### Baseline observations

The initial ARM64 CI run passed tiny-model greedy equivalence but failed the
stream-zone diagnostic: the server's default verbosity 3 suppresses native
INFO messages (upstream maps them to level 4). The acceptance test now uses
level 4 for the streaming profile and scans the complete log for actual zone
registration and repeated tensor trace, not merely the loader's summary.
This is a diagnostic correction, not a proof of successful streaming. The
initial verification-enabled dense test reported 28.06 tokens/s without
streaming and 1.27 tokens/s with streaming. Verification performs extra
reads and byte comparisons, so these are not controlled production throughput
measurements. No automatic activation based on these results.

## SMOL-01 Patch Catalogue & r6 Conflict Audit (2026-11-27)

`experiments/smolmax/full-v1.patch` contains **36 files / 126 hunks**:

- **MoE Pager & Weight Streaming (4 files, 16 hunks)**: `ggml/src/ggml-cpu/moe-pager-tv.inc`, `ggml/src/ggml-cpu/stream-tv.inc`, `src/llama-model-loader.cpp` (6), `src/llama-model.cpp` (6), `src/llama-model.h` (1).
- **Repack Cache & Quantization Kernels (10 files, 59 hunks)**: `repack.cpp` (22), `repack-{q1,q1lut,q20c,q2k,tq}-tv.inc` (5), `ops.cpp` (14), `ggml-cpu.c` (7), `ggml-cpu.cpp` (3), `ggml-cpu-impl.h` (2), `ggml-cpu.h` (1), `binary-ops.cpp` (1), `unary-ops.cpp` (1), `ggml/src/ggml.c` (1).
- **Attention, DeltaNet, Graph & Reduced-Head Architectures (11 files, 42 hunks)**: `llama-arch.{cpp,h}` (3), `llama-context.cpp` (3), `llama-graph.{cpp,h}` (5), `models/{bailingmoe2,bailingmoe3,delta-net-base,granite-hybrid,granite-swa,qwen3,qwen35,qwen35moe,qwen3next}.cpp` (34).
- **Vulkan & Benchmark Tools (3 files, 9 hunks)**: `ggml-vulkan.cpp` (3), `llama-bench.cpp` (1), `perplexity.cpp` (5).

**Patch apply check (`llama.cpp v1.0.0`, `b1b770d90d94cad9e9ea5b993bdbc383a522fb28`)**:
- Standalone against pinned upstream: **PASS** (`git apply --check` clean across all 36 files / 126 hunks).
- Combined with active `r6` patch sequence (`unix-rpc`, `arm32-rpc-wire`, `parallel-rpc-loader`, `rpc-command-batching`, `optimized-rpc-backend-search`, `native-token-activity`): **CONFLICT** at `src/llama-model-loader.cpp:1742` (`parallel-rpc-loader.patch` modifies the same loader section).
- Therefore, Smolmax remains an isolated opt-in candidate profile (CPU-only, single slot, no RPC, no global promotion) with explicit `InspectCapabilities` and strict `ValidateRepackCache` binding metadata (`gguf_sha256`, `architecture`, `quant`, `runtime_sha256`, `platform`, `mode`, `max_bytes`).
