# Model Hub — funcionamento técnico

O Model Hub pesquisa modelos GGUF pela API REST pública do Hugging Face. A CLI Go, o painel Web do Host e a interface Flutter reutilizam `internal/catalog`; o Android utiliza um adaptador Kotlin e pode consultar o catálogo estimado do Host pareado.

## CLI

```sh
nodexlm hub search --query "qwen" --json
nodexlm hub search --query "qwen" --server https://HOST:8443 --ca server.crt
nodexlm hub download --repo OWNER/REPOSITORY --file MODEL-Q4_K_M.gguf --dir ./models
nodexlm hub import --file /caminho/model.gguf --dir ./models
```

Quando a pesquisa utiliza a capacidade do Host, somente Guests RPC prontos entram na estimativa distribuída. Isso não garante que o driver real suporte todas as operações do modelo.

## Android

A aba **Modelos** permite pesquisar, baixar com progresso/cancelamento, selecionar e importar GGUF. No modo **Guest RPC**, esses arquivos locais não são obrigatórios: o Host gerencia o GGUF e transmite tensores. Na inferência independente, o arquivo é carregado no próprio aparelho.

ARM64 pode incluir CPU e GPU Vulkan como backends RPC, caso o driver seja compatível. ARMv7 usa CPU. A memória GPU de aparelhos integrados pode ser compartilhada com RAM.

## Segurança e limitações

- Downloads explícitos são transmitidos para disco, sem manter o GGUF inteiro na memória.
- O arquivo deve passar pela validação antes de ser selecionado.
- Repositórios autenticados, modelos divididos em múltiplos arquivos e arquivos auxiliares podem exigir fluxo manual.
- Estimativas incluem margem para buffers, mas são apenas orientativas.
- O token administrativo do Host não deve ser enviado ao Guest.

Leia [guia de modelos](GUIA_MODELOS.md) e [guia Android](GUIA_ANDROID.md).
