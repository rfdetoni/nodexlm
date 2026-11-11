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

## CLI local (opcional)

Também é possível usar o motor experimental com uma execução local pontual:

```sh
NODEXLM_EXPERIMENTAL_MISTRALRS=1 nodexlm local \
  --engine mistralrs --mistralrs-bin /caminho/mistralrs \
  --model /caminho/model.gguf --prompt "Explique GGUF"
```

Esse comando não torna o mistral.rs um Guest RPC nem modifica os tokens de
pareamento do Host. A política de segurança do processo local continua a mesma.

## Carregamento paralelo de pesos (Linux RPC)

O NodexLM mantém o arquivo GGUF no Host e utiliza RPC autenticado para
distribuir seus tensores. No runtime nativo verificado **r2**, carregamentos
distribuídos com dois ou mais dispositivos RPC habilitam
`NODEXLM_PARALLEL_RPC_UPLOAD=1` automaticamente. O patch implementa
**um worker persistente por dispositivo** e limita a quatro destinos
simultâneos, com apenas um tensor pendente por worker.

- **Memória:** o fluxo paralelo usa ponteiros do GGUF via **mmap**. Não
  duplica os tensores inteiros em buffers Go. Com `--no-mmap`, a via
  síncrona do llama.cpp continua em uso.
- **Ordenação:** cada Guest recebe seus tensores na ordem original.
  O Host sincroniza todos os workers antes de liberar os mapeamentos
  GGUF ou informar 100% da carga.
- **Isolamento:** um dispositivo lento não bloqueia o envio simultâneo
  para outros, embora um erro definitivo em um Guest ainda invalide a
  carga distribuída.
- **Compatibilidade:** r1 e r2 usam o mesmo protocolo RPC b12255.
  O bundle nativo r2 tem SHA-256 fixado e é instalado em cache separado;
  outras plataformas continuam utilizando seus bundles já verificados.
- **Fallback:** em builds sem patch (ou com apenas um Guest), o fluxo
  sequencial original é mantido. Para diagnosticar diferenças de
  estabilidade ou desempenho, inicie o Host com
  `NODEXLM_PARALLEL_RPC_UPLOAD=0 nodexlm host --debug`.

O cache de tensores já existente pode ser habilitado **explicitamente**
no Guest com `nodexlm guest --backend cpu --rpc-cache --debug`.
Os pesos do modelo podem permanecer em disco no Guest: **não habilite
o cache sem confiar no dispositivo e no armazenamento**. O primeiro
carregamento normalmente precisa transferir os pesos; os seguintes
podem se beneficiar dos hashes e arquivos de cache.

No painel, os contadores `to_guest_bytes` e a média em MiB/s representam
**bytes reais transferidos** ao Guest, não camadas confirmadas. Medir
carregamento frio e repetido antes/depois no mesmo modelo e cluster,
comparando também o uso de CPU, rede e memória.

## Chat: esforço e orçamento de thinking

Na aba **Chat**, selecione **Automático**, **Desativado**, **Baixo**,
**Médio**, **Alto** ou **Muito alto (se suportado)**. O controle repassa
`reasoning_effort` ao llama.cpp, conforme o suporte do template do
modelo; `none` solicita a desativação do raciocínio.

O campo opcional de orçamento em tokens repassa
`reasoning_budget_tokens` (0 = não enviar, permitindo ao modelo
decidir), limitado ao máximo de tokens da requisição. O console
mostra `reasoning_content` em seção recolhível quando o backend
envia esse campo no SSE, sem misturá-lo à resposta final ou ao
histórico da conversa. Modelos que não suportam reasoning podem
ignorar as opções; elas não garantem uma quantidade exata de
raciocínio nem o mesmo comportamento entre modelos.
