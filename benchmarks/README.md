# NodexLM Benchmarks

[Metodologia](METHODOLOGY.md) · [Schema de exportação](result.schema.json)

Use o painel Web > Benchmarks, ou scripts/benchmark-cluster.py para coletar
resultados privados A/B. Exporte JSON público pelo painel e acrescente um
relatório de hardware e condições de teste seguindo a metodologia. Armazene
arquivos revisados em results/. Não há resultados oficiais inventados.

Gere uma página estática sem dependências:

    python3 benchmarks/render.py --output /tmp/nodexlm-benchmarks/index.html

Publique o diretório gerado via GitHub Pages, após habilitar Pages no
repositório. O gerador não envia dados para terceiros.

## CLI e estabilidade

    nodexlm benchmark run --model model.gguf --profile compare --rounds 5 --output private.json
    nodexlm benchmark run --model model.gguf --profile stability --node cluster --minutes 60 --output stability.json
    nodexlm benchmark compare --baseline old.json --current new.json

CLI reports are private (native placement information may identify nodes).
The browser exports a reduced public format. Stability uses one loaded runtime
for the entire timed workload; failures abort rather than invent a success.
Before/after device snapshots are not peak memory readings. Energy is
optional, manually measured across the entire stability run; no energy
sensor is simulated.
