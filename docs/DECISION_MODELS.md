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
- Modelos Ollaya executam inteiros no Host, independentemente do RPC GGUF. Distribuição de modelos ONNX por Guests, descoberta automática e escalonamento por latência exigem um contrato próprio e **não estão implementados**.
- O runtime `mistral.rs` experimental não suporta esta rota. GGUF não compatíveis com System One recebem erro do runtime nativo.
- As mesmas restrições de execução, autenticação e tamanho de corpo do gateway permanecem em vigor.

Referências: [Ollaya API](https://github.com/ollaya-dev/ollaya/blob/main/docs/api.md) e [llama.cpp System One](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md#post-v1systemone-typesafe-compatible-system-one-api).


## Filtro no catálogo web e Android

Na aba **Modelos**, use o filtro **Modelos de decisão (System One)** para exibir candidatos GGUF. No aplicativo Android, marque **Somente modelos de decisão** na biblioteca. A classificação usa palavras reconhecidas no nome do repositório/arquivo (por exemplo, Winnow, Decider ou JevK5), portanto é apenas um indício. Um download não garante que o runtime suporta System One. O catálogo GGUF não distribui os modelos ONNX gerenciados pelo Ollaya.
