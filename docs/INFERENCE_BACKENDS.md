# Motores de inferência e benchmark no NodexLM

## Suporte atual

| Motor | Status | Distribuição entre Hosts/Guests | Hardware |
|---|---|---|---|
| llama.cpp | Padrão e mantido | RPC autenticado pelo NodexLM | Linux, Windows, macOS, Android conforme backend |
| mistral.rs | **Experimental, somente Host** | **Não** usa o RPC GGML/NodexLM | Conforme binário mistral.rs instalado no Host |
| MLC LLM | Avaliação para Android | **Não implementada** no NodeXLM | A investigar: OpenCL/Adreno/Mali |

O backend não pode ser alterado com modelo carregado. Nenhum motor alternativo
compartilha automaticamente tensores com Guests do llama.cpp. O Go permanece
responsável pela API autenticada, coordenação, gerenciamento de modelos e ciclo
de vida dos processos. O motor nativo roda em processo separado.

## Habilitar mistral.rs (opt-in)

1. Instale um binário `mistralrs` compatível com a arquitetura e backend do
   Host, preferencialmente uma versão fixada pelo operador. Consulte
   https://docs.mistralrs.dev/ para opções de compilação/instalação.
2. No **Host**, exporte as variáveis antes de iniciar o NodexLM:

   ```sh
   export NODEXLM_EXPERIMENTAL_MISTRALRS=1
   export NODEXLM_MISTRALRS_BIN=/caminho/absoluto/para/mistralrs
   nodexlm host
   ```

3. Na tela **Inferência**, escolha **mistral.rs — experimental**, desmarque
   **Distribuir o modelo** e use um único GGUF compatível. Mantenha os demais
   controles do llama.cpp em seus valores padrão.
4. Carregue o modelo e teste o chat/API via console autenticado.

**Limite de segurança:** o servidor mistral.rs **não implementa autenticação**,
mesmo aceitando headers `Authorization`. O NodeXLM força `--host 127.0.0.1`,
`--no-ui` e usa uma porta efêmera, enquanto mantém o gateway externo
autenticado. Ainda assim, **outros processos/usuários locais podem acessar
diretamente o serviço enquanto ele estiver carregado**. Não execute esse
modo em Host multiusuário ou não confiável. Por isso o opt-in é obrigatório e
o llama.cpp continua padrão. Não habilitamos code execution, shell ou agent
do mistral.rs.

O adaptador não auto-instala Rust nem baixa modelos de fontes arbitrárias.
Apenas o GGUF local já selecionado é passado por `-f`. GGUF fracionado,
embeddings e opções específicas do llama.cpp são rejeitados nesta etapa,
em vez de serem ignorados silenciosamente. Para CPU, use `gpu_layers=0`;
`auto` deixa o mistral.rs escolher GPU/CPU. O campo `threads` do NodeXLM
não controla o scheduler do mistral.rs. O modelo público continua sendo o
nome GGUF do NodexLM; o adaptador encaminha `model=default` ao motor.

## Benchmark lado a lado

Este benchmark usa o mesmo GGUF, prompt, contexto e saída máxima, inicia
**um motor por vez**, faz uma requisição de aquecimento e mede requisições
sequenciais. Não instala dependências Python, não abre portas públicas,
não executa shells e não reutiliza KV cache entre motores.

```sh
python3 scripts/benchmark-backends.py \
  --model ~/modelos/model.gguf \
  --llama-bin /caminho/llama-server \
  --mistralrs-bin /caminho/mistralrs \
  --cpu --threads 4 --context 2048 \
  --iterations 5 --max-tokens 64 \
  --output benchmark.json
```

Métricas: tempo até o servidor estar pronto, taxa *end-to-end* mediana
(inclui prefill; **não** equivale à taxa de decode isolada), tokens efetivos
reportados pelo motor e RSS do processo principal em Linux (não inclui
VRAM, subprocessos e nem Guests). Em Windows/macOS o RSS pode ser
indisponível. O benchmark **não** determina qual motor é mais rápido em
distribuição entre máquinas. Para isso, compare separadamente 1 Host, 1 Guest,
2 Guests e cluster completo com o teste RPC e as mesmas configurações.

Os resultados dependem da versão de cada motor, drivers, quantização e
temperatura do dispositivo. Não publique um ganho em porcentagem antes de
obter amostras reais repetidas em hardware semelhante.

## Android / MLC LLM — trilha separada

MLC LLM possui suporte a GPUs móveis Adreno e Mali via OpenCL, mas não é um
servidor de tensor RPC compatível com o llama.cpp. O caminho de adoção é:

1. Prova de conceito **local** em Android com modelo compatível com MLC.
2. Comparar TTFT, tokens/s, RSS/PSS, memória GPU, energia, temperatura e
   comportamento ao apagar a tela; exigir testes em Android 10+.
3. Definir protocolo de execução de camadas/estado KV antes de qualquer
   inferência híbrida com outros nós.
4. Só então habilitar esse motor nos APKs. O backend Android atual não mudou.

Fontes da interface e limites:
- https://docs.mistralrs.dev/reference/cli/serve/
- https://docs.mistralrs.dev/reference/http-api/
- https://docs.mistralrs.dev/guides/perf/distributed-inference/
- https://github.com/mlc-ai/mlc-llm
