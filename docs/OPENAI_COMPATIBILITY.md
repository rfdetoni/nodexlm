# OpenAI: clientes de chat e agentes

O Host expõe a inferência em `http://127.0.0.1:8787/v1`. Configure um modelo GGUF residente e uma `NODEXLM_API_KEY` exclusiva de inferência, com pelo menos 32 caracteres. O token de administrador também funciona, mas aplicações de chat e agentes devem usar a chave restrita.

A interface preserva os campos JSON e o streaming do runtime `llama.cpp b12255`, incluindo chamadas de ferramentas e raciocínio devolvido pelo modelo. Não reduz mensagens a texto nem converte chamadas de ferramentas em uma resposta textual. Cada modelo residente mantém seu processo/KV; o modelo principal também pode distribuir camadas para Guests RPC.

## Contratos

| Endpoint/recurso | Comportamento |
|---|---|
| `GET /v1/models` e `GET /v1/models/{id}` | Descoberta dos modelos residentes; 404 estruturado para ID ausente |
| `POST /v1/chat/completions` | Mensagens system/developer/user/assistant/tool, conteúdo textual em string/partes, `content:null` em chamadas de ferramentas |
| Ferramentas | `tools`, `tool_choice`, `parallel_tool_calls`, argumentos incrementais, IDs e resultados de ferramentas preservados |
| Clientes antigos | `functions`, `function_call`, role function e respostas correspondentes traduzidos nos dois sentidos |
| Structured Outputs | `response_format` JSON/JSON Schema, inclusive `strict`, encaminhado ao gerador de gramática nativo |
| Amostragem | `temperature`, `top_p`, penalidades, `seed`, `stop`, logprobs e demais campos de inferência chegam ao runtime; a capacidade efetiva depende do modelo/runtime |
| Limites de saída | `max_completion_tokens` aceito e convertido ao parâmetro nativo; `max_tokens` legado continua disponível; enviar ambos é erro |
| Thinking / effort (Chat Completions) | `reasoning_effort` pode ser `none`, `minimal`, `low`, `medium`, `high`, `xhigh` ou `max`; `reasoning_budget_tokens` é opcional. Validação ocorre antes do encaminhamento. O template Jinja do GGUF determina se os níveis geram comportamento distinto. |
| `POST /v1/completions` | Contrato nativo de completions, inclusive streaming |
| `POST /v1/responses` | Input textual/itens, instruções, ferramentas, formato de saída, uso e eventos SSE nomeados |
| Continuação | `previous_response_id` recupera input/output anteriores, isolados pela credencial; instruções anteriores não são reinseridas |
| Histórico Responses | GET/DELETE por ID e GET `/input_items`, com ordem, limite e cursores |
| `POST /v1/embeddings` | Vetores em lote, float/base64 e uso de tokens; exige um modelo dedicado de embeddings carregado |
| Contagem | POST `/v1/responses/input_tokens` e extensão `/v1/chat/completions/input_tokens` do runtime |
| Erros | Objeto error com message/type/param/code; erros após início do SSE são eventos, sem conclusão fictícia |
| Cancelamento | Desconexão cancela a chamada nativa; unload também interrompe a geração ativa |

O Host habilita Jinja explicitamente para parsing/templating de ferramentas. A compatibilidade de transporte não transforma um modelo sem treinamento de ferramentas em um bom agente. Escolha um GGUF de instruções com template apropriado.

As funções são executadas pelo cliente/agente. O servidor não executa comandos, acessa arquivos ou instala integrações com base no nome de uma ferramenta solicitado pelo modelo.

## SDK Python

```python
import os
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:8787/v1",
    api_key=os.environ["NODEXLM_API_KEY"],
)
print([item.id for item in client.models.list().data])
model = "SEU_MODELO_CHAT.gguf"

result = client.chat.completions.create(
    model=model,
    messages=[{"role": "user", "content": "Olá!"}],
    max_completion_tokens=256,
)
print(result.choices[0].message.content)

response = client.responses.create(model=model, input="Olá!", max_output_tokens=256)
continued = client.responses.create(
    model=model,
    previous_response_id=response.id,
    input="Continue a conversa.",
    max_output_tokens=256,
)
print(continued.output_text)
```

Para controlar thinking no chat local, o painel oferece **Automático**, **Desativado**, **Mínimo**, **Baixo**, **Médio**, **Alto**, **Muito alto** e **Máximo**, além de orçamento de tokens (0 omite a restrição por pedido). As preferências de effort/orçamento ficam apenas no `localStorage` desse navegador e não mudam as configurações globais do Host. Um GGUF sem suporte de template à distinção de esforços pode ignorar os níveis; a presença da opção não garante raciocínio interno, exposição do traço ou ganho de qualidade. A API valida números e combinações incompatíveis (por exemplo, `none` com orçamento positivo); o runtime fixa as regras efetivas.

Exemplo com o SDK OpenAI para llama.cpp:

```python
result = client.chat.completions.create(
    model=model,
    messages=[{"role": "user", "content": "Resolva o problema."}],
    reasoning_effort="high",
    max_completion_tokens=1024,
    extra_body={"reasoning_budget_tokens": 256},
)
```

Para streaming, use `stream=True` em Chat Completions/Responses ou `client.responses.stream(...)`. Os eventos preservam tipo, índices, IDs e deltas de argumentos. O adaptador corrige o snapshot inicial `output:null` de versões nativas para `output:[]`, necessário ao acumulador do SDK.

## Agents SDK

```python
import os
from openai import AsyncOpenAI
from agents import Agent, Runner, OpenAIResponsesModel, function_tool, set_tracing_disabled

set_tracing_disabled(True)  # execução local sem enviar traces ao serviço OpenAI
client = AsyncOpenAI(
    base_url="http://127.0.0.1:8787/v1",
    api_key=os.environ["NODEXLM_API_KEY"],
)

@function_tool
def somar(a: float, b: float) -> float:
    """Soma dois valores numéricos."""
    return a + b

agent = Agent(
    name="Assistente",
    model=OpenAIResponsesModel(model="SEU_MODELO.gguf", openai_client=client),
    tools=[somar],
)
result = Runner.run_sync(agent, "Use a ferramenta para somar 12 e 30.")
print(result.final_output)
```

`OpenAIChatCompletionsModel` também é aceito. Use modelos/limites compatíveis com o contexto disponível. Integrações que permitam configurar base URL e chave podem usar o mesmo Host; a aceitação de uma integração específica exige exercitar seus fluxos.

## Embeddings

Carregue um GGUF dedicado (por exemplo, BGE), inclusive como modelo residente adicional. Metadados GGUF de pooling reconhecidos ativam `--embedding` somente naquele processo. Isso permite manter chat e embeddings em processos distintos no mesmo Host. Um modelo de chat sem pooling apropriado não passa a fornecer embeddings por usar o mesmo endpoint.

```python
vectors = client.embeddings.create(
    model="embedding.gguf",
    input=["Primeiro documento", "Segundo documento"],
    encoding_format="float",
)
```

## Persistência, limites e escopo

- Responses com store padrão/true ficam em RAM por até uma hora, com teto global de 256 respostas/32 MiB contabilizados. Entradas antigas podem ser removidas ao atingir o teto. Reiniciar o Host limpa esse histórico.
- `store:false` não retém input/output. O cliente pode manter seu histórico e reenviar os itens em cada chamada. Não há persistência de prompts em disco por essa API.
- Acesso ao histórico exige a mesma credencial que criou a resposta. A chave de inferência não ganha acesso a configurações, pareamento, revogação ou métricas administrativas.
- Requests têm teto de 8 MiB; resposta JSON, 16 MiB; evento SSE, 1 MiB; geração, quatro minutos. O contexto e as capacidades do GGUF/runtime continuam sendo limites reais.
- `target_node` continua como extensão para workers independentes de texto. Ferramentas/Responses usam o modelo do Host, que pode distribuir a execução por RPC. O JNI Android independente mantém seu contrato textual anterior.
- Imagem, áudio, vídeo, arquivos de entrada, Realtime, batch, fine tuning e ferramentas hospedadas OpenAI não são fornecidos. Web search, computer use e file search devem ser implementados como funções do cliente, se necessários. Background/Conversations hospedadas retornam erro explícito; use input reenviado ou previous_response_id.
- Não existe uma alegação de paridade com todos os serviços/modelos hospedados pela OpenAI, nem de compatibilidade garantida com todo cliente futuro. O escopo verificado é chat textual, ferramentas, agentes, Responses e embeddings locais.

## Verificação reproduzível

`go test -race ./...` cobre os contratos e as fronteiras; `go vet ./...` verifica o código Go. `scripts/test-openai-sdk.py` executa SDK OpenAI 3.46.0 e Agents SDK 0.24.0 contra o Host e GGUFs reais, sem chamar a nuvem OpenAI. O CI fixa revisão/SHA-256 dos modelos Qwen2.5-0.5B-Instruct e BGE e usa o bundle nativo já aprovado.

A aceitação exercita descoberta, autenticação, mensagens developer/partes textuais, amostragem, SSE/usage, ferramenta e retorno, argumentos incrementais, JSON Schema, clientes antigos, Responses/streaming acumulado, continuação, exclusão/store:false, agentes completos em ambos os adaptadores e embeddings em lote/base64. O E2E RPC existente continua validando o modelo distribuído e o pool.

Evidência de CI da implementação: [RPC, navegador e aceitação dos SDKs aprovados](https://github.com/rfdetoni/nodexlm/actions/runs/42511723643). [Build e publicação dos sete destinos aprovados](https://github.com/rfdetoni/nodexlm/actions/runs/42511723599). O fluxo usa inferência e ferramentas reais; não substitui testes em todos os modelos, clientes ou aparelhos.
