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
