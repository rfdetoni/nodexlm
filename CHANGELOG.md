# Histórico de alterações — NodexLM

O projeto está em fase alpha. Este registro descreve alterações do código, não uma certificação de execução em dispositivos físicos. As releases `nodexlm-edge` e `android-debug` são experimentais.

## Em desenvolvimento — 2026-11-06

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
