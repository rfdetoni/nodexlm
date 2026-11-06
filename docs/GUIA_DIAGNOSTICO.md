# Diagnóstico e solução de problemas

## Comandos básicos

```sh
nodexlm version
nodexlm host
# Em outro terminal do Host:
nodexlm web-token
```

No Guest, use `nodexlm guest --backend cpu` para começar por um caminho mais simples.

## Guest Linux: libggml.so.0 não encontrada

Esse erro significa que a biblioteca GGML compartilhada não está acessível ao carregador dinâmico ou que o runtime foi extraído de forma incompleta.

1. Atualize a CLI pelo [instalador](../INSTALL.md).
2. Encerre a execução antiga e reinicie `nodexlm guest --backend cpu`.
3. Se persistir, procure as bibliotecas ao lado do executável RPC em `~/.nodexlm/runtimes/llama.cpp/b12143/linux-amd64/cpu-rpc/`.

Caso o diretório do runtime esteja incompleto e precise recriá-lo, o comando abaixo remove **apenas** o runtime baixado, não as credenciais do Guest:

```sh
rm -rf "$HOME/.nodexlm/runtimes/llama.cpp/b12143/linux-amd64/cpu-rpc"
nodexlm guest --backend cpu
```

Não remova `~/.nodexlm/agent.json` como tentativa de corrigir dependências nativas.

## O Host não aparece na descoberta

Verifique que o Host está ativo, que ambos estão na mesma LAN e que os firewalls permitem UDP **46465** e TCP **8443**. Redes com isolamento de clientes Wi-Fi podem bloquear descoberta. Use:

```sh
nodexlm guest --server https://IP_DO_HOST:8443
```

## RAM/VRAM ausentes

Reinstale a CLI recente e reinicie o Guest. As métricas são enviadas na inscrição e nos heartbeats. Alguns drivers não informam VRAM livre; GPUs integradas podem usar RAM compartilhada. Um valor não informado não deve ser substituído por estimativa inventada.

## Android não permanece conectado

Ative o Guest RPC na aba **Cluster**, confirme a notificação de serviço ativo e permita funcionamento em segundo plano nas configurações de bateria. Para examinar logs:

```sh
adb logcat -s NodexLM-RpcGuest:* NodexLM-RPC:* AndroidRuntime:E
```

Se a GPU Vulkan não aparecer, escolha **Automático** ou **Somente CPU**. ARMv7 não inclui Vulkan nesta versão.

## APK não instala ou não abre

A instalação requer Android 10+ e a arquitetura adequada. Um APK com certificado debug diferente do aplicativo instalado pode falhar na atualização. A remoção da versão anterior pode apagar os dados locais. Se o aplicativo fechar ao abrir, obtenha o log:

```sh
adb logcat -c
adb shell am start -n dev.nodexlm.app/.MainActivity
adb logcat -d -s AndroidRuntime:E
```

## Modelo não carrega

Confira se os Guests RPC estão conectados, se a versão do protocolo é `b12143`, se o GGUF é válido e se há memória para pesos, KV cache e buffers. Reduza o contexto ou escolha quantização menor; a soma teórica de memórias não basta para garantir o carregamento.

Ao abrir um problema no GitHub, envie versão, sistema, arquitetura, modo CPU/GPU, log **sem tokens** e passos para reprodução.
