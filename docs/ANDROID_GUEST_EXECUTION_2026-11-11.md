# Android Guest — resultado incremental de implementação (2026-11-11)

Base original: `962af888cc227a43961aeca889f45d976ef934f8`. PR #43 publicada na main em `9714d824cfb7e67aea4436dcaff4d5a325a1dece`. Continuação no PR #44. Esta revisão preserva Go, llama.cpp/GGML e o RPC sobre túneis TLS autenticados com socket privado. **Não é aceite integral do backlog AND-01–AND-14.**

| ID | Situação | Implementado / limite concreto |
| --- | --- | --- |
| AND-01 | implementado sem validação física | Foreground `connectedDevice` e lock Wi-Fi ajustado por API; tela apagada, Doze e ROMs OEM continuam sem prova física. |
| AND-02 | implementado sem validação física | Orçamento de memória compartilhada limitado à RAM física, limite do usuário, reserva do OS, CPU/GPU sem soma; LMK histórico no Android 11+. Admissão inicial usa estimativa limitada de KV/contexto/batch, aplicada tanto à prévia quanto à carga local/distribuída; alocação nativa exata, arquiteturas não usuais e cargas residentes concorrentes continuam sem garantias. |
| AND-03 | corrigido e validado | `Managed.ensure()` invalida subprocesso reaped e não reutiliza endpoint morto; teste real com subprocesso que terminou, sem segundo `Wait()`. |
| AND-04 | implementado sem validação física | Bateria, carregamento, status térmico e headroom disponíveis no heartbeat; parada SEVERE anterior preservada. Drenagem limitada a 90s para violações previsíveis e térmica MODERATE, estado/motivo autenticado no Host, bloqueio de novas colocações, sem reinício automático; redução dinâmica de threads ainda não existe. A emergência SEVERE continua imediata. |
| AND-05 | implementado sem validação física | O modo speed admite Host-only quando pesos/contexto cabem localmente e não há Guest com benchmark comparável mais rápido; modo capacity mantém RPC obrigatório. Mesma decisão na prévia/carga, com teste de regressão. Ainda faltam benchmarks físicos de topologias. |
| AND-06 | implementado sem validação física | Antes de publicar prontidão, o JNI tenta inicializar Vulkan e libera o backend de prova. Auto usa CPU se a inicialização Vulkan falhar; GPU-only/hybrid falham. O heartbeat usa o backend nativo selecionado; não há benchmark sustentado, validação real de grafos, nem recuperação comprovada de crash de driver. |
| AND-07 | implementado sem validação física | A prévia informa a release fixada e não anuncia/ativa automaticamente o patch paralelo sem capacidade declarada no manifesto. Candidato r2 não promovido nem aferido. |
| AND-08 | não confirmado | Contrato `cache_dir` upstream confirmado, mas escreve arquivos por hash sem quota/expulsão garantida. Cache Android permanece desligado para não esgotar armazenamento; faltam implementação nativa segura e testes frio/quente. |
| AND-09 | implementado sem validação física | Threads CPU ajustáveis, limitadas a 8 e ao número de núcleos; falta benchmark sustentado/topologia. |
| AND-10 | implementado sem validação física | Retentativas com jitter, pausa sem rede, parada em 401/403 e espera maior em 503; testes de interrupção do Wi-Fi e drenagem permanecem pendentes. |
| AND-11 | implementado sem validação física | Novo relatório sanitizado por ação Copiar diagnóstico (en/pt-BR), eventos persistidos sem credenciais/prompts; estado draining/motivo e backend selecionado visíveis no Host. A informação local é o último evento registrado, não prova de processo ativo. Falta correlação fase/HELLO depois de reconexão em aparelho. |
| AND-12 | implementado sem validação física | Regressões de processo morto, memória compartilhada, contexto alto, seleção Host-only/Guest obrigatório e heartbeat drenando; Go race/vet e Android UI CI. A matriz física e testes nativos de ROM/driver continuam faltando. |
| AND-13 | implementado sem validação física | Mantidos workflows Go/UI/APK e assinatura persistente por secrets. Adicionado gate de ZIP alinhado e segmentos ELF ARM64 para páginas de 16 KiB no build debug/release. Build completo do APK nesta revisão ainda precisa ser executado e passar; assinatura release depende de secrets. |
| AND-14 | pendente externo | Documento [ANDROID_BACKEND_FEASIBILITY.md](ANDROID_BACKEND_FEASIBILITY.md) fundamenta suporte na revisão fixada e procedimentos. OpenCL/Adreno e Hexagon/NPU continuam desabilitados sem hardware, SDK, verificação do RPC e benchmark comparável. |

## Testes e medições

- Verificar PR Go: `go test -race ./... -timeout 120s`, `go vet ./...` e `gofmt -l .`.
- Verificar UI Android: recursos pt-BR/en-US, build Kotlin e testes locais; não equivale a build JNI nativo para ambas as ABIs nem ao teste em dispositivo.
- Em qualquer aparelho: executar `adb logcat -v time -s NodexLM-RpcGuest:* NodexLM-RPC:* AndroidRuntime:E`, confirmar parada manual, tela apagada, térmica, Wi-Fi, Doze, token revogado e limite de RAM.
- Usar `scripts/hardware-acceptance.py` com mesmo GGUF/hash/contexto/batch/prompt; comparar configurações com amostras reais e registrar aquecimento e memória.

**Benchmark antes/depois: não executado.** Não há métricas válidas de aparelho físico nesta PR. Não inferir ganho de tokens/s de heartbeat, compilação ou quantidade de túneis.

## Compatibilidade e segurança

Campos novos de Device/GPU são opcionais em JSON; o Host continua aceitando Guests sem esses campos. Em Android legado a memória GPU é tratada como compartilhada. Em desktop com VRAM explicitamente dedicada, o orçamento permanece diferente do de memória compartilhada. Não se alteram certificados fixados, tokens, tráfego binário RPC nem streaming OpenAI. O processo Android `:rpc` separa o ciclo de vida da UI, mas **não é isolamento de segurança por UID**.

Releases nativas mantêm `rpc_release=nodexlm-rpc-b12255-r1`. O patch candidato r2 não pode ser anunciado como validado antes de publicar hashes auditados e executar interoperabilidade Host/Linux/Android.

## Pendências para aceite integral

P1: validar estimativa de KV/contexto/batch contra o backend real e arquiteturas diferentes; reconciliar reservas concorrentes; garantir estado e causa da parada do Android no Host (inclusive falha térmica/rede); esgotar matriz de regressões de túnel e process exit.

P2: benchmarks comparáveis de topologia Host-only/Guest, Vulkan CPU/híbrido sustentados e fallback após crash; cache RPC opt-in com quota, auto-tuning CPU, diagnosticar protocolo nativo pós-reconexão, build APK completo em ambas ABIs e assinatura persistente.

P3: estudo de OpenCL/Adreno/Hexagon com configuração concreta de driver/SDK e prova de RPC, não apenas inferência local.

Este documento registra limites da entrega, não substitui issues nem afirma conclusão integral.

## Continuação — PR #45 (2026-11-12)

Implementações adicionais, com aceite de código condicionado aos workflows do PR:

| ID | Alteração | Limite de verificação |
| --- | --- | --- |
| AND-02 | O Android reserva pelo menos o limiar `ActivityManager.MemoryInfo.threshold` da ROM, além da reserva configurada. Se o SO anunciar `lowMemory`, o Guest anuncia `draining` antes de encerrar, preservando sessões existentes até o limite temporal. | Não equivale a medição das alocações reais GGML/KV nem previne toda morte por LMK. |
| AND-04 | `ThermalRecovery` exige 30 s continuamente frio **e sem sessão RPC reivindicada** para reabrir os túneis após status MODERATE. O serviço ainda encerra após 90 s sem recuperação ou imediatamente em SEVERE; política de bateria/horário permanece stop-only. | Android FGS/Doze e temperatura física precisam de testes em aparelhos reais. Não existe ajuste de threads a quente. |
| AND-05 | O planejador exclui Guests com orçamento de RAM zerado sem eliminar os Guests saudáveis. Modo capacity mantém todos os *elegíveis*; IDs duplicados continuam rejeitados. | Não é otimização preditiva de tokens/s. |
| AND-10 | O Host rejeita novos `CONNECT` de Guest em `draining` (HTTP 503) sem restaurar online. Linux/Unix Guests param de repetir conexões após HTTP 401/403; 503 usa atraso exponencial com jitter e limite 30 s. Sessões em uso continuam até terminar ou sofrer timeout. | Verificar perda de Wi-Fi, token revogado e reconexão em instalações reais. |
| AND-12 | Testes Go para exclusão de RAM zerada, drenagem, recusas TLS 401/403/503, backoff; testes Kotlin para histerese e relógio. | Checks Go, UI e compilação JNI não simulam ROM/OEM. |
| AND-13 | O PR #44 anterior **passou** no workflow Android APK ARMv7/ARM64 e verificação 16 KiB. O PR #45 executa novamente esses gates para a nova implementação. | APK de produção assinado por credencial permanente e testes físicos continuam pendentes. |

Outra correção de capacidade Host/Linux: a saída `nvidia-smi` com framebuffer medido e válido passa a marcar VRAM como dedicada, evitando classificá-la como heap integrado. Dados inválidos são descartados; memória compartilhada de Android/iGPU continua sem soma com RAM.

**Permanecem sem implementação/aceite:** cache de tensores Android com quota real, patch de upload paralelo r2 promovido com hashes/interop, telemetria nativa de alocação/concorrência, recuperação pós-crash do driver, auto-tuning sustentado e aceleradores OpenCL/Hexagon reais. Os testes físicos e comparativos precisam ser executados em dispositivos e rede reais, sem inventar números.
