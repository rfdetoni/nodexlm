# NodexLM Benchmarks — metodologia v1

## Comparação reproduzível

Use a mesma revisão e SHA-256 de cada GGUF/shard, parâmetros de inferência,
hardware, runtime e backend. Registre versão/commit NodexLM, versão nativa,
threads, contexto, batch, microbatch, KV, flash attention, GPU layers e estratégia.
Registre RAM/VRAM físicas e se compartilhadas, rede (Ethernet/Wi-Fi, RTT,
throughput direcional), temperatura, alimentação e throttling. Não some RAM
com VRAM compartilhada. Não publique IPs, chaves, nomes de host ou IDs.

| Perfil | Procedimento |
|---|---|
| Speed | Host CPU/GPU e llama.cpp nativo; prefill, decode e ponta a ponta separados |
| Capacity | Modelo que não cabe no Host; registre o erro local e execução distribuída |
| Scaling | Host + 1/2/4/8 Guests; CPU, GPU, Android; registre placement real |
| Efficiency | Energia total via medidor externo, RAM/VRAM e temperatura por nó |
| Stability | 60 min, tela Android desligada, desconexão/reconexão; registre falhas |

Baseline CLI: use llama-bench da mesma revisão (consulte --help) e salve JSON.
Não compare diretamente taxa nativa com taxa ponta a ponta. Rode testes
externos equivalentes somente se modelo, hardware, contexto e parâmetros forem
compatíveis. Use a ferramenta scripts/benchmark-cluster.py para A/B automatizado.
O painel usa um warmup e três requisições de até 64 tokens por rodada;
o perfil ampliado acrescenta workloads de contexto. A/B mede Host uma rodada
e cluster 2–10 rodadas, com placement congelado. Para estudos formais, faça
execuções independentes e alterne a ordem A/B; não trate mediana de três
como p95. Registre todos os resultados, inclusive falhas e casos sem suporte.

## Publicação

Exporte JSON do painel, revise dados pessoais e acrescente metadados físicos
em um arquivo Markdown associado. Não publique relatórios privados do script
sem anonimização. O JSON público do painel exclui IDs e dados de transporte
brutos. O fingerprint não comprova identidade dos pesos; registre SHA-256
separadamente. Use o gerador estático para publicar tabelas dos resultados.
Não inclua resultados sintéticos na página oficial. Resultados enviados pela
comunidade são autodeclarados até reprodução independente.
