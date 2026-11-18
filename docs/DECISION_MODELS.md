# Modelos de decisão (System One)

O NodexLM expõe `POST /v1/systemone` e seu alias `POST /v1/decisions`, protegidos pela mesma `NODEXLM_API_KEY` usada para inferência OpenAI. As respostas são decisões tipadas com probabilidades (`choice`, `score` e `noul`), **não texto gerado**.

## GGUF nativo — llama.cpp

O runtime oficial fixado pelo NodexLM já inclui o endpoint System One. Carregue um GGUF compatível com modelos de decisão no Host, como qualquer outro modelo residente, e use o nome exato apresentado na lista de modelos. O NodexLM não converte arbitrariamente modelos de chat em classificadores: o próprio runtime deve reconhecer a arquitetura especializada. O Host preserva o planejamento RPC e o controle de memória existentes; se o modelo estiver distribuído, a execução seguirá as limitações e custos do llama.cpp RPC.

## Modelos ONNX e famílias adicionais — Ollaya local

O suporte opcional ao [Ollaya](https://github.com/ollaya-dev/ollaya) permite usar suas famílias ONNX e GGUF sem copiar os algoritmos de pontuação/calibração. Instale e execute o Ollaya **separadamente**, no **mesmo Host**. O NodexLM não instala runtimes nem baixa modelos automaticamente ao receber requisições.

Exemplo de inicialização do NodexLM (Ollaya deve estar ouvindo no endereço configurado):

```sh
export NODEXLM_OLLAYA_URL=http://127.0.0.1:11435
# Apenas se o Ollaya estiver configurado para exigir uma chave:
# export NODEXLM_OLLAYA_API_KEY="chave-independente-do-nodexlm"
nodexlm host
```

Os modelos do Ollaya usam o prefixo `ollaya/` no campo `model`. Por exemplo, para usar o modelo remoto `laya:multilingual`, envie `"model": "ollaya/laya:multilingual"`. O prefixo é removido antes da chamada ao Ollaya. Modelos GGUF locais **não** usam esse prefixo.

O endereço `NODEXLM_OLLAYA_URL` precisa ser uma origem HTTP numérica loopback (`127.0.0.1` ou `[::1]`). O gateway recusa URLs externas, redirecionamentos e proxies de ambiente. A credencial enviada pelo cliente ao NodexLM nunca é repassada ao Ollaya. Caso ele requeira autenticação, configure a chave **separada** `NODEXLM_OLLAYA_API_KEY`.

## Exemplo

```sh
curl -sS http://127.0.0.1:8787/v1/systemone \
  -H "Authorization: Bearer $NODEXLM_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "ollaya/laya:multilingual",
    "state": "Cobrança duplicada, preciso de um reembolso urgente",
    "questions": {
      "urgent": {
        "type": "noul",
        "instructions": "Este pedido é urgente?"
      },
      "team": {
        "type": "choice",
        "instructions": "Qual equipe deve receber o ticket?",
        "criteria": {
          "financeiro": "Reembolsos e cobrança",
          "suporte": "Problemas técnicos"
        }
      }
    }
  }'
```

A resposta preserva o JSON retornado pelo backend (incluindo `answers` e probabilidades). O gateway valida limites e tipos básicos, porém **não recalcula** probabilidades ou aplica thresholds/políticas automaticamente. Decisões sensíveis exigem regras de aplicação independentes da predição.

## Limites atuais

- O Ollaya precisa estar instalado e executando localmente; esta integração não gerencia seu processo, seus downloads ou sua lista de modelos.
- A API System One não é streaming. Não há conversão para `chat/completions`.
- Sem configurar workers, modelos Ollaya executam inteiros no Host. Com workers `decision` autenticados e modelos anunciados, os pedidos System One podem ser enviados inteiros a Guests Windows/macOS/Linux ou Android. O runtime ONNX/Ollaya local precisa estar disponível no próprio Guest: o NodexLM ainda não embute esse executor no APK.
- O runtime `mistral.rs` experimental não suporta esta rota. GGUF não compatíveis com System One recebem erro do runtime nativo.
- As mesmas restrições de execução, autenticação e tamanho de corpo do gateway permanecem em vigor.

Referências: [Ollaya API](https://github.com/ollaya-dev/ollaya/blob/main/docs/api.md) e [llama.cpp System One](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md#post-v1systemone-typesafe-compatible-system-one-api).


## Filtro no catálogo web e Android

Na aba **Modelos**, use o filtro **Modelos de decisão (System One)** para exibir candidatos GGUF. No aplicativo Android, marque **Somente modelos de decisão** na biblioteca. A classificação usa palavras reconhecidas no nome do repositório/arquivo (por exemplo, Winnow, Decider ou JevK5), portanto é apenas um indício. Um download não garante que o runtime suporta System One. O catálogo GGUF não distribui os modelos ONNX gerenciados pelo Ollaya.


## Cluster de decisões por tarefa

O cluster possui **dois mecanismos independentes**: distribuição de tensores GGUF pelo protocolo RPC já existente; e o novo `decision-worker`, que despacha uma requisição System One inteira a um nó autenticado.

O Host registra os modelos exatos anunciados pelos workers (`decision_models`). O scheduler utiliza ocupação, CPU, memória, temperatura e bateria, com preferência adicional por menor p50 medido; ele não encaminha jobs a um dispositivo cujo anúncio tenha expirado. Jobs concorrentes são distribuídos entre nós disponíveis. Em falhas ou timeouts limitados, o Host tenta o próximo candidato, depois o runtime local já configurado. As predições são retornadas sem modificar as probabilidades do executor.

### Windows, Linux e macOS

No dispositivo, faça o pareamento normal com o Host, mantenha um runtime System One local (Ollaya ou `llama-server` com modelo compatível), e execute:

```sh
nodexlm node \
  --decision-model ollaya/laya:multilingual \
  --decision-backend http://127.0.0.1:11435
```

Se o runtime local exigir autenticação, configure no dispositivo o segredo opcional `NODEXLM_DECISION_API_KEY` (não use a chave do Host).

Para um GGUF System One compatível servido localmente, informe o modelo GGUF e a porta de seu `llama-server`, sem o prefixo `ollaya/`. `--model` e `--decision-model` podem ser usados no mesmo processo `node` para disponibilizar chat e decisões (se ambos os runtimes estiverem disponíveis).

O worker anuncia o modelo somente quando a API de modelos local o reconhece. Tanto `/v1/models` no formato Ollaya (`models[].name`) quanto o formato OpenAI (`data[].id`) são aceitos. A conexão worker-Host mantém a autenticação/certificado do NodexLM; a conexão do worker ao runtime local aceita apenas IP numérico loopback HTTP, sem proxy, redirecionamento ou URL arbitrária recebida do Host.

### Android

No APK, abra **Processamento → Worker de decisão**, informe o nome exato do modelo, como `ollaya/laya:multilingual`, e a porta local do runtime (padrão 11435). Salve, use **Testar runtime** e inicie o worker de inferência do telefone. Se o runtime exigir uma chave de API, digite-a no campo correspondente; ela é armazenada criptografada no Android Keystore e nunca é enviada ao Host. Para liberar o segredo há um botão próprio. Nenhum modelo é anunciado quando o runtime estiver ausente.

**Pré-requisito importante:** a versão atual do APK **não inclui um motor ONNX ou Ollaya embutido**. É preciso executar separadamente um runtime System One compatível com Android **no próprio celular**; o Host não pode enviar requisições para URLs arbitrárias em outros dispositivos. O Android continua podendo participar do RPC GGUF existente, mas o aplicativo alterna esses modos de processamento para evitar disputar a memória do aparelho. A funcionalidade de task dispatch está pronta; empacotar e distribuir um runtime ONNX Android continua uma etapa separada.

### Monitoramento e segurança

- O dashboard **Decisões → Executores conectados** e `GET /api/decision-workers` (admin-only) exibem modelo, sistema, ocupação e latências p50/p95.
- Os resultados devem ser JSON tipado válido. Cancelamento, timeout, limites de entrada e autenticação de nós são preservados. O sistema não transforma scores em decisões de autorização.
- Os modelos não são automaticamente copiados entre nós; o modelo precisa estar disponível no executor escolhido.
- Os workers atuam somente na API de decisão, sem executar ferramentas ou ações externas. Não há suporte a inferência ONNX dividida por camadas.


### Android — ONNX integrado

O APK contém ONNX Runtime e tokenizador Hugging Face para bundles **Laya** (`laya-markers-v1`). Importe localmente o modelo, o tokenizador e a calibração em **Processamento → Worker de decisão → Importar arquivos do modelo ONNX**. Nenhum servidor Ollaya separado é necessário para executar Laya. O modo HTTP loopback continua opcional para outros executores. [Guia de ONNX no APK](ANDROID_EMBEDDED_DECISIONS.md).
