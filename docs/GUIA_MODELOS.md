# Guia de modelos GGUF e Hugging Face

O Model Hub utiliza a API pública do Hugging Face para localizar GGUF compatíveis. A compatibilidade é **estimada** a partir do tamanho do arquivo e da memória dos dispositivos; não substitui validação com o driver e o modelo real.

## Painel Web

1. No Host, execute `nodexlm host` e abra **http://localhost:8787**.
2. Entre com o token retornado por `nodexlm web-token`.
3. Em **Modelos**, pesquise por nome, projeto ou quantização.
4. Utilize **Baixar**, **Baixar e aplicar** ou **Aplicar** para arquivos já baixados.
5. Confira barra de progresso, tamanho transferido e compatibilidade estimada.

## CLI

```sh
nodexlm hub search --query "qwen" --json
nodexlm hub download --repo OWNER/REPOSITORY --file MODEL-Q4_K_M.gguf --dir ./models
nodexlm hub import --file /caminho/para/model.gguf --dir ./models
nodexlm models --dir ./models
```

A CLI e o painel usam o mesmo núcleo Go de catálogo/download. O Android usa uma implementação nativa Kotlin para busca e importação, sem copiar todo o GGUF para a RAM durante o download.

## Distribuição ou modelo local?

- **Guest RPC Android/Linux:** use o GGUF armazenado no Host; os Guests recebem operações/tensores.
- **Inferência independente:** um dispositivo carrega o próprio GGUF e executa uma solicitação inteira.
- Quantizações menores podem reduzir a demanda de memória, mas o contexto e os buffers aumentam a necessidade total.

Modelos privados, arquivos GGUF fragmentados e recursos adicionais podem exigir integração específica. Não trate uma recomendação de memória como garantia de execução.

Veja [Model Hub técnico](MODEL_HUB.md) e [MoE](MOE.md).
