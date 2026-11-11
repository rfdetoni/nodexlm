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
3. Se persistir, procure as bibliotecas ao lado do executável RPC em `~/.nodexlm/runtimes/llama.cpp/b12255/linux-amd64/cpu-rpc/`.

Caso o diretório do runtime esteja incompleto e precise recriá-lo, o comando abaixo remove **apenas** o runtime baixado, não as credenciais do Guest:

```sh
rm -rf "$HOME/.nodexlm/runtimes/llama.cpp/b12255/linux-amd64/cpu-rpc"
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

Confira se os Guests RPC estão conectados, se a versão do protocolo é `b12255`, se o GGUF é válido e se há memória para pesos, KV cache e buffers. Reduza o contexto ou escolha quantização menor; a soma teórica de memórias não basta para garantir o carregamento.

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
`llama-server` e se Host e Guests usam o mesmo runtime RPC `b12255`.
Compare `nodexlm version` nos dispositivos. Não exclua credenciais ou o
runtime apenas por esse texto; as falhas de modelo devem ser diagnosticadas
pelo erro de carregamento, não por fechamento de socket isolado.

## Host: `Remote RPC server crashed or returned malformed response`

O erro `recv failed (bytes_recv=0, size_to_recv=8)` durante
`load_model` significa que o Host não recebeu o cabeçalho de resposta ao
comando `HELLO` do GGML. Não comprova, sozinho, que o servidor nativo
do Guest caiu.

Verifique, no mesmo momento, as mensagens do **Host**:
- `RPC relay timed out waiting for an idle Guest tunnel`: o Host solicitou
  outra conexão, mas os túneis estavam ocupados ou desconectados.
- `RPC tunnel capacity reached`: o Guest ultrapassou o limite por nó.
- Saída inesperada do `ggml-rpc-server`: verificar o stderr do Guest.

O padrão Linux passa a reservar 8 túneis, dentro do limite do relay.
Para investigar protocolos e desempenho de um modelo novo, reproduza primeiro
com um GGUF pequeno e um Guest CPU; se o mesmo `HELLO` falhar, o problema
não depende do tamanho ou da quantização do modelo. Compare as versões do
runtime `llama.cpp b12255` nos dois lados.

## `RPC tunnel capacity reached` e `RPC tunnel refused: HTTP 503`

O Host limita as conexões CONNECT autenticadas por nó a 8. O código
anterior só liberava uma vaga quando uma inferência utilizava o túnel;
se o Guest desconectasse antes disso, a vaga permanecia ocupada e os
novos slots recebiam `HTTP 503`, mesmo após reiniciar o Guest.

O Host agora observa o fechamento de cada túnel ocioso, remove a
conexão da fila e libera sua vaga. A observação para assim que o
túnel é entregue ao GGML; não há leitura paralela de bytes RPC.

Se o Host continuar em `capacity=8` após a atualização, confira
se existem **dois Guests executando ao mesmo tempo com o mesmo
pareamento**: ambos compartilham o mesmo identificador do nó e o
limite de 8 conexões. Pare o processo antigo e reinicie o Host e o Guest
uma vez para descartar os túneis de uma versão anterior.

## Transportes e bundles atuais

Os bundles r3 fixados incluem Linux amd64 CPU/Vulkan, Linux arm64 CPU, macOS arm64 Metal e Windows amd64 CPU. CPU Linux amd64 foi compilado em Ubuntu 22.04; Vulkan amd64 utiliza o ambiente Ubuntu 24.04 e exige libc/driver compatíveis. `auto` continua escolhendo CPU no Linux/Windows e Metal no macOS.

POSIX com bundle r3 utiliza socket Unix privado. Runtimes customizados precisam anunciar suporte Unix para seleção automática; `nodexlm host --rpc-transport tcp` permite interoperabilidade explícita com runtime legado. Windows mantém TCP loopback. Não abra a porta nativa na LAN.

Se o mDNS não estiver disponível, informe o endereço HTTPS explicitamente. UDP 46465 continua disponível; DNS-SD usa UDP 5353 e a rede pode bloquear multicast.

## Guest recebe HTTP 401 no heartbeat

Se `nodexlm guest --backend cpu` informar `server HTTP 401: unauthorized`, o Host não reconhece mais a credencial de nó salva. A conexão de rede pode estar íntegra; trocar backend CPU/GPU não corrige um token revogado. Gere um código novo no Host (`nodexlm pairing`) e, no Guest, use `nodexlm guest --rejoin --server https://IP:8443 --code CODIGO_NOVO --backend cpu`. A renovação só substitui credenciais após autenticação criptográfica pelo código e TLS. Não remova o diretório de modelos ou runtimes. A mensagem `recv returned 0` isolada pode ser uma sondagem de prontidão local, não uma falha de credenciais.

## Transferência RPC não começa: 33–42 bytes e nenhum tensor

Se `/api/status` indicar `loading_model` por vários minutos, mas
`rpc_transfer[*].to_guest_bytes` mostrar apenas dezenas de bytes, as
conexões foram abertas **sem comprovação de upload dos pesos**. A quantidade
de túneis `connected` ou o heartbeat não provam execução de inferência.

1. No Host, use `nodexlm host --debug`. O log registra o planejamento,
   início do `llama-server` e os volumes reais encaminhados.
2. Liste os Guests com o token local do Host, sem compartilhá-lo:
   ```sh
   curl -fsS -H "Authorization: Bearer $(nodexlm web-token)" \
     http://127.0.0.1:8787/v1/nodes \
     | jq -r '.[] | [.id[:8], .name, .platform, .runtime.version, .runtime.rpc_ready] | @tsv'
   ```
3. Identifique um Guest que tenha `to_guest_bytes` pequeno, mas
   `from_guest_bytes=0` e `idle < connected`. Reinicie **apenas ele**
   com `nodexlm guest --backend cpu --debug` e examine
   `~/.nodexlm/logs/guest.log`. No Android, use `adb logcat` conforme
   a seção acima.
4. Para testar sem esse Guest **sem revogar credenciais**, reinicie o Host
   passando seu ID completo (64 caracteres hexadecimais):
   ```sh
   nodexlm host --debug \
     --rpc-exclude-ids ID_COMPLETO_DO_GUEST
   ```
   Múltiplos IDs podem ser separados por vírgula. Esta opção só filtra
   **novas seleções de camadas**, não encerra nem revoga sessões existentes;
   exige reiniciar o Host e repetir o carregamento. Se o restante do
   cluster não tiver memória suficiente, o planejador retorna erro de
   capacidade; experimente um GGUF menor para isolar a conexão.

Uma sessão pode ficar bloqueada em `HELLO` por versão de RPC diferente,
Guest nativo travado ou falha no transporte. Não infira a causa apenas pela
quantidade de bytes. Reproduza com **um Guest por vez** e compare os logs
em ambos os lados. O `llama-server` agora preserva tanto stdout quanto
stderr no arquivo de debug do Host, em vez de descartar stdout.


## Android: Host encaminha 33 bytes ao Guest, sem resposta nativa

Quando um Android aparece em `rpc_transfer` com `to_guest_bytes` pequeno,
`from_guest_bytes=0` e uma conexão ocupada, isso significa que houve
uma tentativa de RPC, **não** que os pesos foram distribuídos. O diagnóstico
Android distingue o início do túnel do recebimento de uma resposta do GGML:

- `RPC tunnel N claimed by Host`: o Host selecionou a sessão TLS
- `RPC tunnel N connected to native GGML RPC server`: o aplicativo alcançou o socket Unix privado
- `RPC tunnel N delivered first Host request`: o primeiro pedido chegou ao socket local
- `RPC tunnel N received first native response`: o GGML respondeu pela primeira vez
- `native server has not replied after ...`: houve pedido sem resposta após 20s; apenas alerta, sem encerramento automático

Conecte o celular ao computador com depuração USB autorizada:

```sh
adb devices
adb logcat -v time -s NodexLM-RpcGuest:V NodexLM-RPC:V AndroidRuntime:E
```

Inicie o Guest RPC na aba **Cluster** e tente um carregamento do Host.
Para testar o Redmi 9 ou outro aparelho com Vulkan instável, troque
temporariamente o backend do Android para **Somente CPU**, pare e reinicie
o Guest RPC antes de repetir. A seleção `Auto` pode habilitar simultaneamente
CPU e Vulkan; os logs devem indicar se o problema é conexão TLS, socket
Unix ou ausência de resposta do servidor nativo.

A versão exibida em `runtime.version` é a revisão de llama.cpp (`b12255`),
não o número do APK. Confirme que o APK instalado é realmente o build
atualizado, pois duas versões distintas do NodexLM podem anunciar a mesma
revisão de runtime.

## Linux: timeout de 3 minutos ao gerar resposta com RPC

Se o chat exibir `context deadline exceeded (Client.Timeout or context
cancellation while reading body)`, isso indica o término de uma requisição
HTTP interna à inferência. Não prova, isoladamente, perda dos túneis RPC.

O cliente HTTP do llama.cpp não deve ter prazo global fixo: a requisição de
chat já possui contexto com prazo e cancelamento explícitos. O NodexLM usa
até 4 minutos para chat local e até 20 minutos para chat distribuído;
as aplicações OpenAI que se conectam ao Host podem definir um prazo próprio.

Antes de iniciar o modelo distribuído, o Host agora testa os comandos nativos
`RPC_CMD_HELLO` e `RPC_CMD_DEVICE_COUNT` em cada Guest selecionado, com
limite de 6 segundos por Guest (em paralelo). As mensagens do Host informam
o identificador do dispositivo que não respondeu, recusando o carregamento
antes de iniciar uma inferência bloqueada. Essa verificação não baixa o
modelo nem envia tensores; ela soma **42 bytes de requisição e 48 bytes de
resposta** quando ambas as trocas são concluídas com sucesso.

Acompanhe os logs:

```sh
nodexlm host --debug --web-addr 127.0.0.1:8788
tail -F ~/.nodexlm/logs/host.log
```

Em cada Guest Linux, confirme que o processo anterior foi encerrado antes
de reiniciar em modo debug:

```sh
nodexlm guest --backend cpu --debug
tail -F ~/.nodexlm/logs/guest.log
```

Se a preflight indicar `Guest <ID> cannot complete GGML RPC HELLO/DEVICE_COUNT`,
verifique o log desse Guest, sua versão de runtime e se há um worker nativo
atolado. Uma opção de isolamento é iniciar o Host com
`--rpc-exclude-ids ID_COMPLETO`, sem revogar o pareamento, e repetir com
um modelo GGUF menor. Se o HELLO passar mas o carregamento ainda parar
após algumas dezenas de bytes, verifique `rpc_transfer` e o log nativo
do `llama-server`: nesse caso o problema está além da preflight.

A passagem de `HELLO` confirma apenas a disponibilidade inicial do
worker. Não garante RAM suficiente para o modelo de 27B, transferência
de pesos ou desempenho de inferência.
