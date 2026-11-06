# Guia do Guest Linux

O Guest RPC recebe do Host tensores e operações correspondentes às camadas atribuídas. Não é necessário armazenar o GGUF completo no Guest.

## Instalar e parear

```sh
curl -fsSL https://raw.githubusercontent.com/rfdetoni/nodexlm/main/install.sh | sh
nodexlm version
nodexlm guest
```

Na primeira execução, o comando procura Hosts na LAN e solicita o código de 8 caracteres. Depois de parear, as credenciais são reutilizadas.

Para escolher CPU ou informar manualmente o Host:

```sh
nodexlm guest --backend cpu
nodexlm guest --server https://192.168.1.100:8443 --backend cpu
```

O backend `vulkan` depende de GPU/driver compatível e, conforme a plataforma, de um runtime compilado separadamente. O modo `auto` usa CPU como caminho seguro padrão em Linux.

## RAM e VRAM

O Guest coleta RAM do sistema e dados disponíveis da GPU e envia heartbeat ao Host a cada 30 segundos. Alguns drivers e GPUs integradas não fornecem métricas confiáveis de VRAM; nesse caso o valor pode permanecer desconhecido. A RAM reportada durante o pareamento pode mudar depois da inicialização.

No Host, confira os Guests na página **Cluster** e verifique se o dispositivo aparece como RPC pronto.

## Falha por libggml.so.0

Atualize o executável e reinicie o Guest:

```sh
curl -fsSL https://raw.githubusercontent.com/rfdetoni/nodexlm/main/install.sh | sh
nodexlm guest --backend cpu
```

O NodexLM procura bibliotecas GGML ao lado do `ggml-rpc-server`, sem `sudo` nem configuração global de `LD_LIBRARY_PATH`. Caso o pacote esteja incompleto, consulte o guia de diagnóstico para baixar novamente **somente** os binários do runtime.

## Recomendações

Mantenha a mesma revisão de protocolo `llama.cpp b12143` entre Host e Guests e prefira rede Ethernet para cargas maiores. A divisão de camadas por RPC depende da largura de banda e pode ser mais lenta que a execução em um único computador.

Leia [Instalação](../INSTALL.md) e [diagnóstico](GUIA_DIAGNOSTICO.md).
