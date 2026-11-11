# Android Guest — resultado incremental de implementação (2026-11-11)

Base: `main` em `962af888cc227a43961aeca889f45d976ef934f8`. Alterações na PR #43. Esta revisão preserva Go, llama.cpp/GGML e o RPC sobre túneis TLS autenticados com socket privado. **Não é aceite integral do backlog AND-01–AND-14.**

| ID | Situação | Implementado / limite concreto |
| --- | --- | --- |
| AND-01 | implementado sem validação física | Foreground `connectedDevice` e lock Wi-Fi ajustado por API; tela apagada, Doze e ROMs OEM continuam sem prova física. |
| AND-02 | implementado sem validação física | Orçamento de memória compartilhada limitado à RAM física, limite do usuário, reserva do OS, CPU/GPU sem soma; LMK histórico no Android 11+. Admissão exata por KV/contexto/batch e cargas concorrentes ainda exige implementação/validação. |
| AND-03 | corrigido e validado | `Managed.ensure()` invalida subprocesso reaped e não reutiliza endpoint morto; teste real com subprocesso que terminou, sem segundo `Wait()`. |
| AND-04 | implementado sem validação física | Bateria, carregamento, status térmico e headroom disponíveis no heartbeat; parada SEVERE anterior preservada. Redução gradual, drenagem e cooldown ainda não implementados. |
| AND-05 | não confirmado | Seleção ainda não compara Host sozinho em velocidade usando benchmark comparável de topologia. |
| AND-06 | não confirmado | O modo automático nativo ainda não mede desempenho sustentado nem recupera crash Vulkan; GPU-only continua falhando explicitamente quando a GPU está ausente. |
| AND-07 | implementado sem validação física | A prévia informa a release fixada e não anuncia/ativa automaticamente o patch paralelo sem capacidade declarada no manifesto. Candidato r2 não promovido nem aferido. |
| AND-08 | não confirmado | Cache persistente opt-in de tensores não implementado; faltam contrato comprovado, quota, limpeza e controle por Host/revisão. |
| AND-09 | implementado sem validação física | Threads CPU ajustáveis, limitadas a 8 e ao número de núcleos; falta benchmark sustentado/topologia. |
| AND-10 | implementado sem validação física | Retentativas com jitter, pausa sem rede, parada em 401/403 e espera maior em 503; testes de interrupção do Wi-Fi e drenagem permanecem pendentes. |
| AND-11 | implementado sem validação física | Limites e bateria/temperatura transportados ao Host, revision/capability na prévia; falta relatório sanitizado completo e confirmação de protocolo HELLO/DEVICE_COUNT após cada reconexão. |
| AND-12 | implementado sem validação física | Regressões de processo morto e memória compartilhada adicionadas. Não há teste físico Android; reproduzir matriz abaixo. |
| AND-13 | já atendido e revalidado | Revisados workflows separados Go, UI Android e APK nativo com assinatura persistente opcional e verificação de proveniência. Build Android completo em release e assinatura dependem do fluxo/secrets. |
| AND-14 | pendente externo | OpenCL/Adreno e Hexagon/NPU exigem aparelho/driver/SDK compatível, evidência RPC e benchmark; não habilitados sem prova. |

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

P1: validar contexto e batch altos com KV real; reconciliar reservas concorrentes; garantir estado e causa da parada do Android no Host (inclusive falha térmica/rede); esgotar matriz de regressões de túnel e process exit.

P2: seleção Host sozinho com medições comparáveis, benchmark real CPU/Vulkan/híbrido e fallback consciente, cache RPC opt-in protegido por quota, auto-tuning CPU, diagnósticos completos, teste de APK nativo e assinatura reprodutível.

P3: estudo de OpenCL/Adreno/Hexagon com configuração concreta de driver/SDK e prova de RPC, não apenas inferência local.

Este documento registra limites da entrega, não substitui issues nem afirma conclusão integral.
