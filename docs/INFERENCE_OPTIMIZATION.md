# Motor de inferência: estratégias e parâmetros experimentais

O Host e o escalonador continuam em Go. O llama.cpp/GGML permanece responsável
por kernels, alocação real de camadas, KV cache e execução das operações.

## Estratégias

Na tela **Configuração de inferência**, habilite a distribuição e escolha:

- **Capacidade:** mantém a compatibilidade com o comportamento original:
  utiliza todos os Guests RPC prontos e divide camadas conforme memória estimada.
- **Velocidade (padrão):** ordena Guests com base no benchmark *real* do mesmo GGUF
  (quando disponível), RTT e taxa de eco da rede, e usa a menor combinação
  que satisfaz a margem estimada de memória. Guests com links ruins ficam
  atrás, mas continuam elegíveis quando necessários ao modelo.

Use **Medir rede** em cada Guest. Para medir tokens/s individuais, descarregue
modelos e execute o benchmark real selecionando aquele Guest. Métricas com mais
de 24 horas expiram; ausência de medição não significa que o nó seja lento.

Para ponderar camadas, benchmarks de Host e Guest precisam ser controlados,
recentes (menos de 24 horas), do mesmo modelo e fingerprint de opções, e da
mesma base de taxa (decode nativo ou ponta a ponta). Sem comparabilidade,
o split permanece proporcional à memória. Uma medição suspeita de Host/Guest desativa a ponderação por tokens/s para a decisão até nova medição controlada; a amostra de cluster permanece visível para diagnóstico. Com o opt-in explícito no Host
`NODEXLM_RPC_COST_PLACEMENT=1` (desabilitado por padrão) no modo velocidade,
quando todos os nós selecionados possuem RTT medido, o planner compara prefixos ranqueados
e Guests individuais por custo serial estimado: tempo por camada derivado
de tokens/s do modelo inteiro, mais dois RTTs por segmento remoto por token.
A distribuição remota segue a proporção de memória; o cálculo não é medição
de throughput nem garante aceleração. O modo capacidade mantém todos os Guests.

A seleção é executada **antes** do carregamento e reutilizada no endpoint de
prévia `GET /api/plan`. Ao mudar a conectividade entre a prévia e a
aplicação, os resultados podem diferir. A divisão calculada é estimativa,
pois a distribuição final é determinada pelo GGML. Não há troca de camadas
a cada token, nem previsão de tokens/s a partir de RTT/banda.

## Controles nativos opcionais

O formulário expõe os seguintes campos, por padrão sem modificar as opções
do llama-server:

| Campo | Valores | Consideração |
| --- | --- | --- |
| Flash Attention | auto, on, off | `on` exige backend suportado |
| KV cache | f16, q8_0, q4_0 | Quantização reduz memória e pode afetar qualidade; confirme suporte no backend |
| Batch lógico | 0 (nativo), 1–8192 | Aumenta demanda de memória |
| Microbatch | 0 (nativo), 1–4096 | Quando ambos definidos, microbatch não pode exceder batch |
| Especulação | off, ngram, draft | `ngram` usa lookup de tokens; `draft` carrega um GGUF diferente na CPU do Host, com `--spec-type draft-simple` |
| Tokens por draft | 0 (nativo), 1–32 | `--spec-draft-n-max` para draft GGUF ou `--spec-ngram-simple-size-m` para n-gram; janelas grandes podem aumentar latência e consumo |
| Reuso parcial de KV/prompt | 0 (desligado), 1–4096 | `--cache-reuse`, tenta reutilizar trechos em prompts similares; requer prompt caching |
| Cache MoE calculado na CPU | 0–1 | `--moe-cache-cpu-share` define fração dos misses executada na CPU em vez de carregar expert por PCIe; exige `--moe-cache-mib` e MoE em RAM |

As opções de ajuste nativo são **opt-in** e foram verificadas contra a CLI da revisão `940a30ee3b5593e1a66fe0aadc3b731989022552` fixada no repositório. Não são kernels do Basalt e não implementam a migração adaptativa de especialistas; aproveitam controles nativos do llama.cpp. No modo mistral.rs, permanecem incompatíveis.

Para uma RTX 2060 de 6 GiB, comece com cache MoE 0 e CPU share 0, reserve VRAM para pesos e KV, e aumente o cache em pequenos passos somente com telemetria livre. Em nós RPC heterogêneos, o cache MoE pode ser dividido entre GPUs e a melhoria não está garantida. O retorno do `/api/plan` é estimativa, não um teste físico.

Confirme a comparação com `/api/bench/inference` antes/depois, usando
mesmo modelo, contexto, quantidade de tokens e condições de rede. O ganho
deve ser registrado separadamente para **prefill**, **decode**, latência do
primeiro token e carregamento; os benchmarks atuais ainda não cobrem
todos esses valores por nó. Um modelo pode não suportar as opções solicitadas;
nesse caso a inicialização falhará explicitamente.

## Sugestão de n-gram para links RPC

A prévia oferece uma sugestão estruturada quando um Guest com camadas
remotas tem RTT recente medido de pelo menos 5 ms. Nenhuma configuração é
alterada. O autotuning explicitamente solicitado mede candidatos de 4, 8 e
16 tokens usando o mesmo workload e exige mesma base, topologia e aceitação
de pelo menos 25%. A ideia é verificar vários tokens draft em um forward,
amortizando round-trips; o ganho depende da taxa real de aceitação.

O flag n-gram correto na revisão fixada é
`--spec-ngram-simple-size-m` (não `--spec-draft-n-max`, que pertence ao
draft GGUF). O E2E `scripts/e2e-rpc.py` verifica carregamento e geração
combinando n-gram e RPC; resultado ainda precisa ser observado no bundle
b12255/r3. Se incompatível, o load falha explicitamente, sem fallback silencioso.

## Cache nativo de tensores RPC — opt-in

No Linux/macOS/Windows Guest, use:

```sh
nodexlm guest --backend cpu --rpc-cache
```

`--rpc-cache` encaminha `--cache` para o ggml-rpc-server da versão fixada
do llama.cpp. Ele **grava pesos de modelos no disco do Guest** e reduz
retransferência de tensores em recarregamentos; NÃO acelera necessariamente
a geração token a token. Utilize **somente máquinas confiáveis**, com disco
criptografado e controles de acesso ao cache. O padrão permanece desativado.
O local pode ser alterado via `LLAMA_CACHE`, cujo subdiretório `rpc`
é administrado pelo servidor nativo. Revogar um nó não remove automaticamente
o conteúdo armazenado no disco dele. O Android não recebeu essa opção.

## Limites atuais e próximas etapas

- Não há escalonamento dinâmico de especialistas MoE; as opções MoE atuais
  delegam a execução ao llama.cpp.
- O modelo draft independente pode rodar na CPU do Host, mas não está implementada a alocação automática do draft em um Guest. Compatibilidade de vocabulário e custo/benefício precisam ser verificados pelo llama.cpp.
- Não há tensor parallelism adaptativo, nem migração de KV cache entre
  processos, nem balanceamento de requisições entre múltiplos clusters
  distribuídos. Modelos residentes adicionais permanecem locais ao Host.
- Antes de atualizar o llama.cpp fixado, é necessário recompilar os bundles
  de RPC, validar interoperabilidade e executar testes físicos com Linux,
  Windows, macOS e Android (inclusive energia e térmica).
- Mantenha a saída como **experimental**, não prometa melhoria de velocidade
  sem benchmark A/B sobre o mesmo hardware.

## Atualização do motor nativo

A revisão do llama.cpp fica fixada em `internal/llamaruntime/revision.json` e os hashes SHA-256 em `internal/llamaruntime/revision.json`. Ao mudar de revisão, os pares Host/Guest RPC devem ser recompilados juntos (inclusive o patch Unix) e os novos binários publicados e verificados antes de preencher `published`. Enquanto os novos hashes não forem publicados, `EnsureDistributed` tenta a compilação local da mesma revisão. **Não é permitido reutilizar bundles publicados de revisões antigas**. Android compila sua própria biblioteca C++ na revisão fixada e precisa de testes físicos específicos.

## Runtime RPC b12255/r3

O bundle fixado para estes WPs é `nodexlm-rpc-b12255-r3`; a revisão, os hashes e os patches permanecem inalterados. O cache de instalação é separado por tag. Upload paralelo é ativado apenas nos bundles verificados, não representa ganho garantido no decode.

## Microbenchmark RPC por dispositivo (WP3)

`NODEXLM_AUTO_RPC_MICROBENCH=1` habilita a coleta experimental em registro/heartbeat autenticado, somente sem modelos residentes e sem outro load/benchmark. A ação Medir dispositivos RPC permite medir sob demanda. São 8 e 32 MiB por dispositivo, no máximo dois dispositivos, 160 MiB de payload bidirecional e 30 s. SET_TENSOR é seguido de uma barreira RPC: a taxa inclui a sincronização; GET_TENSOR mede readback com verificação de dados. As medições auto são separadas do benchmark manual; MiB/s nunca vira tokens/s. Três amostras de decode ao vivo divergentes em mais de 30% do benchmark de cluster sinalizam possível necessidade de re-medição, não provam regressão. Nenhum benchmark de inferência é iniciado automaticamente.

## Prefill e carregamento (WP4)

Autotune usa RTT >15 ms ou eco <25 MiB/s para testar microbatch 128/256, links rápidos medidos testam 2048; sem amostra mantém 512/1024. Sugestão de prefill exige ganho medido e decode sem regressão acima de 10%; ganho de decode tem prioridade. O patch `parallel-rpc-loader.patch` usa `unordered_map<ggml_backend_dev_t,...>`: conta dispositivos RPC selecionados (não GPU local). Um Guest híbrido pode ativar upload paralelo quando o bundle verificado permite; `NODEXLM_PARALLEL_RPC_UPLOAD=0` desativa. Prévia reavalia q8_0 usando estimativa real de KV (inclui overhead de blocos quantizados); sugere apenas se reduz Guests ou permite GPU-only. Qualidade/Flash Attention devem ser verificadas, nada é aplicado automaticamente.

## Estimativa de bytes por camada (WP5)

`NODEXLM_GGUF_LAYER_BYTES=1` ativa o cálculo experimental por descritores de tensores GGUF, sem carregar pesos. Tipos quantizados conhecidos são contabilizados por tamanho de bloco; embeddings/output e outros tensores não-blk são reservados no Host. Metadados incompletos, tipos desconhecidos e shards mantêm fallback uniforme. A alocação contígua com margem de 15% é estimativa; validação física do tensor split nativo é necessária antes de usar em produção.

## Transporte (WP6)

O marcador 2 legado aceita dois exchanges; o Host usa quatro túneis autenticados separados (warmup descartado + três pares medidos), com mediana, amplitude e deadline total de 15 s. Sem novo marcador. O watcher ocioso bloqueia em Read e o claim interrompe por deadline antes de entregar o socket; TLS 1.3 pode reutilizar sessões por cliente pareado, preservando VerifyConnection e ALPN HTTP/1.1. Medições separadas up/down exigem negociação futura.

## GPU do Host (WP9)

Metal é uma opção explícita para macOS. Em cargas distribuídas e benchmarks de cluster, a enumeração `--list-devices` do próprio llama-server resolvido confirma o nome da GPU Vulkan ou Metal antes de inseri-la no split. Sem confirmação ou com múltiplas GPUs, mantém a distribuição CPU/RPC; não presume Vulkan0 ou Metal0. CUDA e mapeamento multi-GPU ficam fora do escopo. Testes físicos em Mac permanecem necessários.

## Aceitação integrada dos nove WPs

O workflow `Real RPC end-to-end` executa um teste focado nas alterações de E2E, com dois Guests CPU reais no runner e o bundle pinado verificado por SHA-256, mais `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...`. Não recompila nem publica o runtime. Verifica eco mediano, microbenchmark nativo, recusa durante residência e n-gram+RPC. O teste em runner não estabelece aceitação física.

Para a coleta física, mantenha o mesmo GGUF (confira `sha256sum`), configurações, topologia e temperatura inicial. Descarregue todos os modelos e execute `python3 scripts/hardware-acceptance.py --model MODELO.gguf --repeat 3 --output host.json`, depois o mesmo com `--node ID_DO_GUEST --output guest.json` e `--node cluster --output cluster.json`. Para cluster, selecione o GGUF e habilite distribuição; use capacity se precisar forçar o teste distribuído. O coletor salva settings, plano de cluster, métricas nativas, estatísticas RPC antes/depois e rede por Guest. Repita com cada opt-in isolado, mesmo contexto, batch, ubatch, KV/FA e temperatura. Compare apenas bases de taxa e topologias equivalentes. Hardware, consumo e qualidade exigem validação do operador.

A revisão final rejeita bases de taxa mistas na ausência de benchmark do Host, exige fingerprint da carga e topologia compatíveis para o alerta de divergência, não ignora um link lento medido quando outro ainda não tem amostra e contabiliza os pesos não-blk na prévia do Host. O coletor interrompe sessões com topologia alterada entre rodadas.

No autotune de cluster, o placement nativo do baseline fica fixado (Guests, camadas locais, devices e tensor-split). O relatório informa `placement_fixed`; candidatos revalidam os budgets atuais e a estimativa de memória de cada dispositivo, sem recalcular o split por fingerprint. Topologia desconhecida/legacy continua funcionando na inferência normal, mas não produz recomendação de autotune de cluster sem um baseline explícito.

A sugestão de KV q8_0 usa o mesmo snapshot de benchmarks/fingerprint da seleção original exclusivamente na comparação de memória; não assume que os tokens/s se repetem após quantizar o KV e não aplica a configuração.

## Nó principal (opt-in)

A preferência `primary_node` (vazio/auto, host ou ID do Guest conectado) maximiza a fatia de pesos estimada, não tokens/s. Tem precedência sobre ponderação por velocidade/RTT. Em capacidade, mantém todos os Guests com memória disponível; em velocidade pode omitir Guests secundários. Pesos não-blk permanecem no Host. Usa pesos GGUF por bloco quando disponíveis, estimativa uniforme conservadora caso contrário, margem de 15% e reserva de contexto por dispositivo. Legacy sem topologia usa fallback proporcional identificado; principal offline causa erro. O cálculo maximiza dentro da ordem determinística dos Guests (principal primeiro); não busca permutações. O processo llama-server e coordenador não migram. Compare A/B antes de concluir ganho.

A coleta de cluster com 2–10 rodadas usa `/api/bench/inference-series`: mantém os argumentos nativos do primeiro load, descarrega entre rodadas, revalida budgets/topologia e rejeita séries incompatíveis. Rede medida antes da série (baseline compartilhado), contadores RPC antes/depois de cada rodada. A API antiga de medição única continua compatível.

## Migração r4 (candidato)

A v1.0.0 estável do llama.cpp usa RPC 10.0 e remove APIs obsoletas. A release contém apenas um marcador nightly, por isso os binários NodexLM devem ser compilados a partir do commit fixado, validados e ter hashes próprios publicados. Não misturar runtime r3 e r4: atualizar Host, todos os Guests desktop e Android juntos. O preflight rejeita protocolo incompatível antes de enviar tensores. Para rollback, reinstalar o conjunto completo r3. O PR de migração permanece draft até validação e fixação dos hashes; nenhuma alegação de desempenho físico.

### Agrupamento nativo RPC (candidato r5)

`NODEXLM_RPC_BATCH_COMMANDS=1` agrupa até 16 comandos pequenos sem resposta (≤4 KiB de descriptor; ≤64 KiB por lote), somente quando já estão na fila do dispatcher v10. Não espera para formar lotes, não agrupa comandos com resposta/payload externo e não cruza barreiras. O mesmo fluxo de bytes RPC 10 é enviado ao Guest; não exige patch correspondente no worker. Modo padrão intacto, logs detalhados desativam a agregação. Novo bundle r5 separado do r4; não reutilizar hashes do r4. Validar A/B de qualidade, TTFT, decode e CPU em hardware antes de ativar por padrão.
