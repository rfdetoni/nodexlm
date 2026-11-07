# Guia do Host e painel Web

O Host coordena o cluster, gerencia o GGUF e oferece interface administrativa local.

## Inicializar

```sh
nodexlm host
```

Na primeira inicialização, o programa gera credenciais locais, certificado TLS e um código temporário de pareamento.

Abra outro terminal **no Host**:

```sh
nodexlm web-token
```

Acesse **http://localhost:8787** e informe o token. Ele é diferente do código de 8 caracteres exibido no terminal.

## Conectar dispositivos

- Linux: instale a CLI no outro computador e execute `nodexlm guest`.
- Android: abra a aba **Cluster**, informe o código temporário e ative **Guest RPC**.
- Para renovar o código, execute `nodexlm pairing` no Host.

O coordenador usa HTTPS na porta **8443**; a descoberta automática usa UDP **46465**. O mDNS anuncia `nodexlm.local` e `_nodexlm._tcp.local` quando a rede privada IPv4 permite multicast. A descoberta UDP continua disponível. A UI administrativa permanece no loopback e não deve ser publicada na Internet.

## Selecionar e aplicar um modelo

1. Abra **Modelos** no painel.
2. Pesquise modelos GGUF ou selecione um já salvo/importado.
3. Observe tamanho, compatibilidade estimada e progresso do download.
4. Habilite **Distribuir o modelo entre os Guests RPC conectados** antes de carregar o modelo se desejar inferência em múltiplos dispositivos.
5. Carregue o GGUF e verifique as métricas dos Guests.

O Host decide como carregar os tensores conforme o backend GGML e a memória disponível. A quantidade anunciada de RAM/VRAM é uma métrica, não uma garantia de que todo o espaço será utilizável.

## Segurança e manutenção

Não envie `nodexlm web-token`, certificados privados ou dados de `~/.nodexlm` para terceiros. Para revogar um dispositivo, identifique-o por `nodexlm nodes` e utilize `nodexlm revoke --id ID`.

Mais detalhes: [pareamento e segurança](PAIRING.md) e [diagnóstico](GUIA_DIAGNOSTICO.md).

## Modelos residentes, chat e medições

Use **Carregar adicional** para manter outro modelo no Host, respeitando o orçamento de memória e o limite total de quatro. O principal pode distribuir camadas; modelos adicionais usam processos e KV próprios no Host. O GGML fixado aceita apenas um cliente de modelo por Guest, portanto múltiplos modelos distribuídos simultâneos no mesmo Guest não são suportados.

No chat, escolha o modelo residente, temperatura e limite de tokens. As respostas chegam incrementalmente; **Parar** cancela a solicitação. O histórico persiste neste navegador e só é excluído após confirmação. A reutilização de prefixo mantém KV por modelo, sem compartilhar estado entre modelos diferentes.

**Ver plano** mostra camadas, pesos, KV F16 estimado e medições recentes. **Medir rede** mede RTT/eco pelo túnel TLS; **Medir inferência** executa uma carga real e exige descarregar os modelos. Pela CLI: `nodexlm bench --model MODELO [--node ID]`. O gráfico usa amostras medidas; banda de eco não é convertida em tokens/s.
