# Guia do Android: Guest RPC com CPU e GPU

## Compatibilidade

- **Android 10+** com arquitetura ARMv7: Guest RPC **CPU**.
- **Android 10+** com arquitetura ARM64: Guest RPC **CPU** e tentativa de backend **Vulkan**.
- O aparelho precisa fornecer GPU Vulkan e driver compatível com o backend do `llama.cpp b12255` para usar GPU.
- GPUs móveis frequentemente compartilham memória com a CPU. **Não some RAM e VRAM como se fossem capacidades independentes.**

O app é experimental; build aprovado não comprova execução ou desempenho em todos os celulares.

## Instalar e conectar

1. [Baixe o APK de testes](https://github.com/rfdetoni/nodexlm/releases/tag/android-debug).
2. No Linux, execute `nodexlm host`.
3. No Android, abra **Cluster → Conectar ao Host** e informe o código de pareamento temporário.
4. Em **Backend de processamento**, escolha o modo apropriado e pressione **Ativar Guest RPC (sem GGUF local)**.
5. Na notificação do Android, verifique se o Guest permanece ativo. Para interromper, use **Parar Guest RPC**.

O servidor GGML RPC é iniciado em um processo Android isolado e usa um **socket Unix no diretório privado do aplicativo**. O serviço acessa esse socket por `LocalSocket` e recebe chamadas do Host por túneis HTTPS autenticados. O GGUF permanece no Host, e o Android recebe buffers, tensores e grafos.

## Modos

| Opção | Comportamento |
|---|---|
| **Automático** | Tenta GPU Vulkan + CPU; se Vulkan não existir, usa CPU |
| **Somente CPU** | Mantém o processamento na CPU |
| **Somente GPU Vulkan** | Exige que uma GPU Vulkan funcional seja detectada |
| **GPU Vulkan + CPU** | Exige Vulkan e oferece ambos os backends ao Host |

Em aparelhos ARM32 a GPU não está incluída no APK atual: utilize **Automático** ou **Somente CPU**.

## RAM, VRAM e bateria

O heartbeat informa RAM e, quando o driver Vulkan expõe valores, informações de memória GPU. Em GPUs integradas, esse espaço pode ser compartilhado; a indicação é informativa e não garante orçamento adicional.

O Android utiliza um serviço em primeiro plano, wake lock de CPU e bloqueio de economia de energia do Wi-Fi durante a atividade. Isso aumenta o consumo e aquece o aparelho. Algumas ROMs ainda interrompem serviços bloqueados; habilite operação sem restrição nas configurações de bateria se necessário.

### Limites de contribuição e comportamento com a tela apagada

Em **Ajustes**, configure:

- **Contribuição máxima de RAM (MiB):** `0` utiliza o orçamento automático; um valor positivo limita **o quanto este celular pode oferecer ao Host**, não a RAM total do aparelho.
- **Reserva mínima de RAM (MiB):** pelo menos `1024`, preservada antes da margem adicional de 20% para KV e buffers. Uma reserva maior reduz a capacidade oferecida.
- **Threads nativas da CPU:** `0` escolhe um valor conservador; é possível escolher de 1 a 8, limitado aos núcleos disponíveis. Alterações exigem reiniciar o Guest. Mais threads não garantem mais tokens/s.

O heartbeat informa **RAM física disponível também no modo GPU-only**. Vulkan no Android é classificado como memória **compartilhada**, e o Host limita o orçamento ao menor teto físico/configurado conhecido; não soma as mesmas páginas entre CPU e GPU. Os valores são estimativas de admissão, não uma confirmação de que determinado modelo ou contexto cabe no driver. O app registra o histórico de término por falta de memória no Android 11+ quando essa informação está disponível.

O Guest permanece um serviço foreground iniciado pelo usuário, com notificação e botão de parada. No Android 10–13, o lock `WIFI_MODE_FULL_HIGH_PERF` pode ajudar com a tela apagada; no Android 14+, a plataforma converte esse lock em `LOW_LATENCY`, cuja eficácia é limitada à tela ligada/app em primeiro plano. **Nenhum deles garante rede contínua durante Doze, políticas OEM ou desconexões**. O wake lock de CPU não substitui permissão de rede, Wi-Fi nem teste real de tela apagada.

Falhas permanentes de autorização do túnel (HTTP 401/403) interrompem o Guest; perda temporária de rede aplica espera limitada e jitter; saturação HTTP 503 aumenta o intervalo. A reconexão não recupera buffers ou KV cache da sessão RPC perdida.

### Aceite físico reproduzível

Registre modelo/SoC, API, ROM, arquitetura, driver GPU, alimentação e estado da otimização de bateria. Compare o mesmo GGUF/hash, contexto, batch e prompt entre Host sozinho, Host+Linux e Host+Android CPU/Vulkan; capture primeiro token, tokens/s sustentados, aquecimento e memória. Repita com tela apagada por 30–60 minutos, intercale Wi-Fi/Doze e verifique o Host e o Android após falhas. Um teste sintético com `adb shell dumpsys deviceidle force-idle` não comprova comportamento de todas as ROMs.

O coletor já existente pode gerar medições (não inclua tokens de autenticação no relatório):

```sh
python3 scripts/hardware-acceptance.py --model modelo.gguf \
  --node ID_DO_NODE --repeat 10 --output android-cpu.json
```

Teste um aparelho e modo de backend por vez. O relatório do coletor **não constitui aceite físico automático**, e resultados antes/depois só são comparáveis com parâmetros idênticos.

## Quando a GPU não aparece

- Verifique se o dispositivo usa **ARM64**.
- Escolha **Automático** e observe se a interface detecta a GPU.
- Em aparelhos sem Vulkan funcional, o CPU é o fallback correto.
- Com ADB conectado, use `adb logcat -s NodexLM-RpcGuest:* NodexLM-RPC:* AndroidRuntime:E`.

A **inferência independente** continua existindo para GGUF armazenado no telefone, mas não compartilha camadas do modelo do Host.

Veja [Host](GUIA_HOST.md) e [diagnóstico](GUIA_DIAGNOSTICO.md).

## Modelos e energia

Na importação de GGUF dividido, selecione todas as partes do mesmo modelo. A biblioteca só mostra conjuntos completos; o download baixa e verifica as partes antes de publicar a primeira. Downloads Android usam o gerenciador nativo do sistema; não prometem as mesmas garantias de retomada do downloader Go.

Em Ajustes, configure operação somente carregando, bateria mínima e intervalo de horas (inclusive atravessando meia-noite). Temperatura SEVERE interrompe o serviço. A notificação informa o motivo; reiniciar exige ação do usuário. O modo independente suporta texto incremental UTF-8, amostragem, stop e cancelamento de lease; mantém os limites de contexto/saída documentados no código nativo.

As telas e notificações possuem recursos em inglês e português do Brasil, escolhidos pelo idioma do Android. Erros retornados pelo sistema/runtime podem manter sua língua original.

## Android 11/12 não pareia ou aparece offline após conectar

Informe a URL HTTPS do Host (por exemplo, `https://192.168.100.30:8443`) em vez de depender somente da descoberta UDP. Confira hora automática no Android e use um código novo do Host. Se o dispositivo foi pareado com um Host anterior e o certificado mudou, o app agora apresenta uma mensagem específica: importe o certificado `server.crt` do Host pelo botão **Importar certificado do Host** na aba Cluster. O certificado é fixado por SHA-256 e a validação criptográfica do código permanece obrigatória.

Para registrar a etapa exata onde a conexão falha:

```sh
adb logcat -v time -s NodexLM-Pairing:* NodexLM-RpcGuest:* NodexLM-RPC:* AndroidRuntime:E
```

O app registra a etapa de URL, handshake TLS, certificado conhecido, oferta e prova criptográfica; **não registra códigos nem tokens**. Em dispositivos com provedores TLS OEM mais antigos, o túnel RPC aceita ausência do suporte a ALPN e continua exigindo HTTPS autenticado e HTTP/1.1. Não rebaixamos a versão mínima TLS do Host.

Se o pareamento funciona mas o Guest cai offline, escolha **Somente CPU** para isolar problemas Vulkan/driver e confirme a notificação de serviço foreground e permissões de bateria. O teste de compatibilidade física Android 11/12 ainda depende de logs de aparelhos reais.

## Seleção automática de velocidade e retirada por energia (PR #44)

Na estratégia `speed`, o Host agora é candidato a executar sozinho quando os pesos, a estimativa de KV/contexto e a margem de batch cabem na memória local. Uma medição de Guest mais rápida pode justificar o uso distribuído. Na ausência de medições comparáveis, priorizar o Host é uma **heurística de latência**, não prova de aumento de tokens/s. A estratégia `capacity` mantém a distribuição com pelo menos um Guest.

Quando o Android alcança estado térmico MODERATE ou viola a política configurada de bateria, carga ou horário, marca seu estado como `draining`, publica o motivo ao Host e deixa de tentar novos túneis. O Host não inclui esse aparelho em novos planejamentos; sessões nativas já reivindicadas recebem até 90 segundos para encerrar. Estado térmico SEVERE ainda causa desligamento imediato e não há retomada automática: o usuário deve iniciar uma nova sessão quando permitido. Não há ajuste ao vivo de threads ou migração transparente do KV.

Em **Ajustes**, use **Copiar diagnóstico RPC sanitizado** para obter um JSON local com revisão nativa, ABI, memória, política configurada e último evento do serviço. O relatório é criado apenas após ação do usuário, sem token, código de pareamento, certificado, URL do Host ou conteúdo de inferência. **Último evento registrado não comprova serviço ativo.** Revise os dados do aparelho antes de compartilhá-lo.

O modo `auto` tenta inicializar Vulkan antes de selecioná-lo; se a inicialização falhar, pode selecionar CPU. A seleção anunciada não prova execução correta de grafos nem desempenho. Se o processo nativo falhar após iniciar, não há recuperação transparente da sessão; consulte o log. Consulte [viabilidade de OpenCL/Hexagon e cache RPC](ANDROID_BACKEND_FEASIBILITY.md).

O build completo do APK verifica páginas de 16 KiB com `scripts/verify-android-pages.sh` sobre os artefatos gerados; resultados de CI de Kotlin isoladamente não comprovam ELF/zipalign nem comportamento físico.
