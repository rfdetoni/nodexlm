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
3. Se persistir, procure as bibliotecas ao lado do executável RPC em `~/.nodexlm/runtimes/llama.cpp/b12143/linux-amd64/cpu-rpc-r2/`.

Caso o diretório do runtime esteja incompleto e precise recriá-lo, o comando abaixo remove **apenas** o runtime baixado, não as credenciais do Guest:

```sh
rm -rf "$HOME/.nodexlm/runtimes/llama.cpp/b12143/linux-amd64/cpu-rpc-r2"
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

## Guest RPC: `malformed HTTP response "\\x00\\x00\\x1e\\x04..."`

Esses bytes representam um quadro `SETTINGS` do HTTP/2. O Guest estava anunciando `h2` no TLS e enviando `CONNECT` em HTTP/1.1, mas o Host selecionava HTTP/2; o túnel precisa de HTTP/1.1 para `Hijack`.

Atualize **os Guests Linux** e reinicie `nodexlm guest`. Em celulares, instale o APK Android atualizado e reinicie o Guest RPC. As chamadas REST podem continuar usando HTTP/2; apenas os túneis RPC devem negociar `http/1.1`. Não é necessário excluir os certificados ou refazer o pareamento.

```sh
curl -fsSL https://raw.githubusercontent.com/rfdetoni/nodexlm/main/install.sh | sh
nodexlm guest --backend cpu
```

Se o erro continuar, confirme `nodexlm version` e reinicie o processo antigo; envie o log sem credenciais.

## Guests Linux online, mas nenhum RPC autenticado

O dispositivo estar **online** significa que o Host recebe o pareamento ou o heartbeat, não que o GGML RPC está pronto para compartilhar partes do modelo.

1. Atualize a CLI no Host e em cada Guest; encerre e reinicie processos antigos.
2. No Linux remoto, execute **`nodexlm guest --backend cpu`**. Apenas `nodexlm join` ou `nodexlm node` não criam túneis RPC.
3. Confira `NodexLM RPC worker ready` e se há mensagens `RPC tunnel disconnected`. Problemas `libggml.so.0` ou `native RPC server did not start` impedem o Guest RPC.
4. No painel `http://localhost:8787`, a linha do nó deve mostrar **RPC conectado**; o resumo deve indicar mais de zero **Guests RPC conectados**.
5. Só então habilite **Distribuir** e aplique o GGUF.

O túnel binário exige HTTP/1.1 CONNECT e autenticação com certificado TLS fixado; a REST API pode usar HTTP/2. A CLI força HTTP/1.1 para RPC. Falhas de protocolo ou de runtime nativo devem ser corrigidas, não mascaradas tratando nós pareados como RPC prontos.

## Guest RPC: reconexão lenta ou worker nativo encerrado

O Guest mantém até oito túneis HTTP/1.1 autenticados por certificado. Cada
reconexão falha usa um intervalo progressivo limitado a 10 segundos; depois de
uma sessão concluída normalmente, o intervalo volta a 1 segundo. A negociação
TLS tem limite de 5 segundos e a resposta ao CONNECT, de 10 segundos.

Se o servidor GGML nativo encerrar inesperadamente, o comando Guest agora
termina com erro em vez de deixar túneis reconectando sem backend. Nesse caso,
verifique primeiro o stderr do executável RPC e a compatibilidade da versão
com o `llama-server` do Host.

O indicador RPC disponível significa **túnel autenticado ativo**, e não apenas
heartbeat ou cadastro do dispositivo. Para eliminar erros de negociação de
versões antigas, atualize o Guest e reinicie o processo; não é necessário
refazer o pareamento.

## Guest GGML: `Accepted client connection` seguido de `recv returned 0`

O servidor GGML registra esse texto quando uma conexão TCP local é encerrada
sem um comando RPC adicional. **Uma ocorrência é esperada** durante o teste de
prontidão do Guest: a CLI abre uma conexão a `127.0.0.1:50052` e a fecha.

Também podem surgir conexões curtas quando o `llama-server` do Host está
consultando dispositivos RPC ao aplicar um modelo. As mensagens isoladamente
não comprovam incompatibilidade de protocolo nem erro de inferência.

Se o modelo não carregar ou os Guests não processarem camadas, confira os logs
do **Host** no momento em que aplicar o GGUF. Verifique a mensagem de erro de
`llama-server` e se Host e Guests usam o mesmo runtime RPC `b12143`.
Compare `nodexlm version` nos dispositivos. Não exclua credenciais ou o
runtime apenas por esse texto; as falhas de modelo devem ser diagnosticadas
pelo erro de carregamento, não por fechamento de socket isolado.
