# smolmaxxing no NodexLM — integração experimental

Fonte: https://github.com/hyperionaidev/smolmaxxing (MIT); alterações nativas
derivadas do llama.cpp também são MIT. Modelos mantêm suas próprias licenças.

## O que este PR efetivamente integra

- Perfis opt-in de inicialização para **pager MoE** ou **streaming de pesos**
  em um llama-server **externo reconstruído e explicitamente registrado**.
- Verificação SHA-256 do executável antes de habilitar opções experimentais.
- Validação de modelo MoE, GPU=0, Linux/Android, ausência de RPC distribuído
  e limites de memória/prefetch; o motor de produção não muda.
- Seleção opcional de threads conforme o cluster big.LITTLE do sysfs.
- Testes determinísticos para perfis, manifestos, limites e topologia.

**Não incluído:** o port C/C++ do pager, repacking Q2_K/Q1_0, kernels NEON,
DeltaNet fused, page cache, MTP patch, streaming efetivo, cache de
especialistas no Guest, integração com APK/Web ou escalonamento de experts
entre Guests. As variáveis só têm efeito se um llama-server já tiver essas
modificações. O llama.cpp b12255 da main NÃO as incorpora. Não confunda
interface de configuração com implementação da inferência.

## Uso opt-in, quando existir um binário nativo realmente portado

1. Opcionalmente compilar um runtime **separado** usando os commits fixados
   do upstream (não substitui o motor distribuído b12255):

```sh
scripts/build-smolmax-native.sh /opt/smolmax
```

   Isso aplica a versão original do patch ao llama.cpp 8642541. Este caminho
   precisa de compilador e validação com GGUF reais; não garante suporte a
   todas as flags do launcher do NodexLM. Alternativamente, portar cada
   patch para b12255, com testes específicos de GPU/RPC.

2. Para um binário recompilado manualmente, registrar com:

```sh
python3 scripts/register-smolmax-runtime.py \
  --binary /opt/smolmax/llama-server --features moe_pager weight_streaming
```

3. Apontar o NodexLM para o binário registrado com
   `nodexlm host --llama-bin /opt/smolmax/llama-server` (não `auto`) e
   configurar GPU layers **0** e distribuição RPC **desativada**.
4. Para MoE (GGUF com `expert_count`), o tamanho do pool deve ser
   configurado explicitamente após avaliar o consumo de RAM:

```sh
NODEXLM_SMOLMAX_MODE=moe NODEXLM_SMOLMAX_EXPERT_POOL=32 \
  NODEXLM_SMOLMAX_PREFETCH_K=0 NODEXLM_SMOLMAX_READERS=2 \
  nodexlm host --llama-bin /opt/smolmax/llama-server
```

5. Para modelo denso maior que a RAM, definir o orçamento residente
   explicitamente (não existe valor seguro universal):

```sh
NODEXLM_SMOLMAX_MODE=stream NODEXLM_SMOLMAX_RESIDENT_MB=512 \
  NODEXLM_SMOLMAX_WINDOW_MB=128 NODEXLM_SMOLMAX_READERS=2 \
  nodexlm host
```

Para verificar tensores, utilizar `NODEXLM_SMOLMAX_VERIFY=1` em uma
execução diagnóstica; custa I/O. O perfil MoE não aplica bias ao roteador,
não altera experts ativos nem faz pruning de vocabulário.

A política de threads é separada do patch nativo:

```sh
NODEXLM_SMOLMAX_THREADS=performance nodexlm host
```

Isso seleciona o número de threads do cluster de maior capacidade
segundo sysfs; **não** fixa afinidade, não acelera automaticamente a GPU
nem se aplica aos threads internos do Guest RPC. Quando a telemetria for
insuficiente, preserva-se o default de 4 threads.

## Como validar uma futura implementação nativa

1. Portar incrementalmente o pager e streaming ao upstream fixado, sem
   misturar seus mecanismos de memória com RPC na mesma execução.
2. Compilar Linux arm64, Linux amd64 e Android arm64 e rodar GGUF reais.
3. Comparar SHA/resultado/perplexidade com stock e usar verify=1.
4. Medir RSS/PSS, page faults, tokens/s nativos, TTFT, UFS/SSD, bateria,
   throttling e correção após evicções, 3+ rodadas por configuração.
5. Integrar cache e prefetch nos Guests somente depois de um protocolo
   que transporte ativação, roteamento e capacidades sem multiplicar RTT.
6. Portar kernels Q2_K/ternários/dotprod por arquitetura e benchmark;
   desabilitá-los em CPU que não oferece as instruções exigidas.
7. Comparar também MTP e speculative decoding no cenário I/O-bound.
8. Só então expor controles no painel e APK.

Não existe ganho de desempenho comprovado por este PR sem um runtime
portado e testes físicos. O registro local de um executável é uma
declaração do operador, não uma assinatura de segurança do fornecedor.

## Perfil diagnóstico e cache de repacking

Em um runtime com os kernels da mesma revisão do patch, pode-se optar por
`NODEXLM_SMOLMAX_OPPROF=1` ou `2` e por
`NODEXLM_SMOLMAX_REPACK_CACHE=/caminho/absoluto/cache.bin`.
O diretório deve existir; não usar caminho relativo nem symlink no destino.
O profiler aumenta o overhead e pode gerar muitos logs. O cache de repacking
consome espaço adicional e somente deve ser reutilizado para o modelo,
quantização e binário exatos que o criaram; limpar ao mudar qualquer um.
**Não habilite estas opções no runtime b12255 sem o port nativo**.

Os valores de pool e memória residente são agora obrigatórios para impedir
que defaults adivinhados provoquem OOM em aparelhos de pouca RAM.
