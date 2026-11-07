# Histórico de alterações — NodexLM

O projeto está em fase alpha. Este registro descreve alterações do código, não uma certificação de execução em dispositivos físicos. As releases `nodexlm-edge` e `android-debug` são experimentais.

## Preparação de release — 2026-11-07

- Identidade Android permanente com backup protegido e bootstrap de secrets sem senha nos argumentos; compilação release minificada verificada com chave de teste quando não há identidade configurada.
- Pipeline Authenticode Windows antes da publicação, timestamp RFC 3161, verificação de assinatura e atualização posterior de ZIPs, checksums e SBOM. Certificado de produção não é simulado.
- Checksums Android separados dos desktop para impedir sobrescrita em uma release compartilhada.
- KDF de pareamento movido para a biblioteca padrão Go; fingerprint completo opcional, certificado Android importado respeitado e leituras limitadas.
- Preview imutável, tap Homebrew e manifests WinGet com URLs/hashes fixados; coletor de evidência de hardware com aceitação física explicitamente não presumida.

## Roadmap implementado — 2026-11-07

- Credencial de inferência nativa removida dos argumentos do processo; ambiente privado e teste de regressão para evitar exposição em listagens.

- Shards completos no Android, verificação em segundo plano protegida contra cancelamento, SSE UTF-8/amostragem/stop e monitoramento de lease no modo independente.
- Pool de quatro modelos, cache por modelo, benchmark real de inferência e plano com KV/métricas observadas. Modelos adicionais usam o Host devido ao limite de um cliente nativo por Guest.
- Contadores de tokens/gerações/erros, histograma de duração, reconexões/túneis e gráfico de latência; descoberta DNS-SD com fallback UDP.
- Patch Unix RPC compilado em Linux, ARM64, macOS, Windows e Vulkan; inferência Unix Linux aprovada no CI. Windows mantém TCP loopback.
- Empacotamento deb/rpm, geração de manifests Homebrew/winget para tags estáveis e SBOM dos módulos Go/pacotes. Docker passa a incluir `go.sum` e baixar os módulos antes do build.
- Guias EN e recursos Android EN/pt-BR. Flutter opcional corrigido para administração em 8787 e recuperação do chat após falha.
- E2E real de navegador em desktop/mobile, multicast em CI, fuzz de pareamento/extração e análise de vulnerabilidades Go sem ocorrências.
- Auditoria PAKE independente, assinatura de produção, aprovação em índices externos e QA físico continuam identificados em [ROADMAP_STATUS](docs/ROADMAP_STATUS.md).

## Histórico — 2026-11-06

### Confiabilidade e operação
- CLI e painel usam um downloader único, com retomada validada por Range/If-Range, SHA-256 incluindo redirecionamentos LFS, espaço livre e retenção de parciais.
- API OpenAI de texto no loopback usa o modelo carregado no Host, com SSE, parâmetros de amostragem e chave de inferência separada; respostas completas preservam usage/finish_reason.
- Worker Go transmite eventos incrementais e cancela seu backend quando o cliente encerra o pedido. Fila com prioridade limitada, cancelamento explícito e notificação por canal.
- Chat do console com histórico local apagável, temperatura, limite de tokens, interrupção e tempos medidos. Prévia de camadas/memória do cluster, com estimativas identificadas.
- `doctor`, benchmark de RTT/banda no túnel TLS e métricas autenticadas em loopback. Comandos administrativos passam a usar a porta 8787 por padrão.
- Revisão nativa centralizada em `internal/llamaruntime/revision.json`; configuração Docker/Compose para Linux, documentação EN e inventário CycloneDX das releases CLI.
- Android: porta RPC aleatória, suporte ao benchmark TLS, perfil de carga/bateria/horário e notificação de interrupção; configuração de páginas nativas de 16 KiB. Exige build/device QA.
- Teste E2E executado em Linux CPU com pareamento real, GGUF de 260K parâmetros, runtime nativo RPC fixado por hash, streaming e chave API restrita. Workflow pesado somente manual.

### Segurança, Windows e ciclo de vida
- Atualizado o compilador das releases Go para 1.27.3, com correções de segurança recentes.
- Reinicializações do Host no Windows deixam de interpretar permissões POSIX sintéticas como ACLs.
- Encerramento HTTPS/Web aguarda `Shutdown` antes de liberar o coordenador RPC.
- Cancelamento de uma geração Android não bloqueia a geração seguinte.
- Testes de PR limitados a mudanças Go, com verificação de race no Linux e reinicialização de segredos no Windows.
- Política de reporte de vulnerabilidades em `SECURITY.md`.

### Estabilidade RPC — Host, Linux e Android
- Limite de tempo para TLS e resposta CONNECT; reconexões progressivas com reinício após sessão bem-sucedida.
- O Guest Linux monitora o processo GGML nativo, rejeita porta RPC ocupada e reserva 8 túneis para picos de descoberta do modelo.
- O Guest Android monitora a thread RPC nativa, encerra o serviço caso ela morra, reserva 8 túneis e reduz as alocações na leitura HTTP.
- Build Android disparado somente por alterações em `android/**` ou no próprio workflow.
- Mensagens GGML de EOF em conexões de sondagem diferenciadas de falhas efetivas do Host; relay registra esgotamento e timeout de túneis.

### Adicionado
- Guest Android RPC com servidor GGML nativo em processo isolado, usando túnel TLS autenticado e certificado do Host fixado.
- Seleção Android de backend: automático (Vulkan + CPU quando disponível), somente CPU, somente GPU Vulkan e GPU + CPU.
- Backend Vulkan na compilação ARM64 e detecção nativa de GPUs, com contadores de memória expostos pelo driver quando disponíveis. ARMv7 continua CPU.
- Catálogo GGUF do Hugging Face para Host, CLI, desktop e Android, com download e importação manual.
- Progresso de download no painel Web, incluindo ações de baixar e aplicar.
- Descoberta LAN e pareamento simplificado com `nodexlm guest`.
- Instaladores POSIX `install.sh` e PowerShell `install.ps1`; distribuição de binários por GitHub Releases.
- Guias em português e README resumido.

### Corrigido
- Túnel RPC Linux/Android força ALPN HTTP/1.1, eliminando leitura incorreta de quadros HTTP/2 como resposta CONNECT.
- Inicialização do serviço de atualizações Android em versões recentes do sistema.
- Manutenção do worker Android com a tela bloqueada por serviço de primeiro plano e wake locks, respeitando limitações do fabricante.
- Relatórios de RAM/VRAM dos Guests Linux no pareamento e nos heartbeats.
- Inicialização Linux do GGML RPC com bibliotecas compartilhadas locais (`libggml.so.0`) usando diretório de bibliotecas do runtime.
- Compilação e empacotamento Android 10+ ARMv7/ARM64.

### Pendente de validação
- Inferência distribuída efetiva em diversos telefones Android 10+, especialmente com Vulkan e em ARM32.
- Capacidade de VRAM, limites térmicos, consumo de bateria e estabilidade com tela bloqueada em cada fabricante.
- Testes funcionais da interface Flutter em desktop.
- Lançamento estável com assinatura Android persistente; builds debug podem exigir reinstalação.

## Regras do histórico

Registre mudanças por função e evidência (compilação, testes automatizados, hardware) antes de promovê-las a uma versão estável. Não confunda aprovação do CI com desempenho comprovado.
