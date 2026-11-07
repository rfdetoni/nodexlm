# Estado do roadmap — 06/11/2026

Base da implementação: `de3c676`, posterior à avaliação estática de `1a477fd`. Este arquivo distingue código implementado de aceitação comprovada. O roadmap original tem 19 itens e **não está integralmente concluído**.

| # | Item | Estado e evidência |
|---|---|---|
| 1 | GGUF em shards | Go/CLI/Host: catálogo, download/importação e tamanho agregado já existiam na base e foram revalidados. Novo downloader e testes cobrem falhas parciais. Android ainda não agrupa/baixa conjuntos completos. |
| 2 | Download robusto | Implementado em pacote compartilhado: Range/If-Range, SHA-256 inclusive nos redirects, espaço disponível, timeout de inatividade, parciais persistentes, publicação exclusiva e bloqueio entre processos. Testes de interrupção, corrupção, Range inválido e fallback do servidor passaram. Checksum só é afirmado quando existe metadado verificável. |
| 3 | API OpenAI SSE | Host nativo e worker Go implementados e testados. Texto, amostragem, lista de modelo, cancelamento e chave separada. E2E nativo passou. Workers Android legados não têm o novo transporte de eventos; ferramentas/imagens e outros campos OpenAI não são suportados. |
| 4 | doctor | Comando texto/JSON implementado; diagnóstico local e sugestões. Firewall/SAN/clock são descritos com seus limites; sem alegar prova de conectividade da LAN. Portas customizadas exigem verificação separada. |
| 5 | Chat do painel | Código implementado com múltiplos turnos, histórico local apagável, parâmetros, SSE, tempos e interrupção. Teste DOM do catálogo e E2E de API passaram. QA visual bloqueada: download do Playwright inválido; Chrome alternativo não inicia por proibição de socket Unix no ambiente. |
| 6 | Plano do cluster | Prévia de camadas e memória implementada a partir de metadados reais. Estimativas identificadas. Ainda não usa o resultado do bench, nem prevê tokens/s ou contabiliza KV cache. |
| 7 | Observabilidade | Métricas de nós/jobs no loopback autenticado, request ID e opção de JSON log implementados. Faltam contadores completos de tokens/reconexão, histogramas e gráfico no painel. |
| 8 | bench | RTT e eco de banda pelo túnel TLS real, resultado persistido pela CLI. E2E passou. Não mede ainda uma carga de inferência por dispositivo nem alimenta o planejador. |
| 9 | Energia Android | Perfil de carga, limiar de bateria, horário, térmico e notificação implementados. Porta nativa aleatória e benchmark adicionados. Compilação APK/JNI e teste em dispositivo ainda pendentes. |
| 10 | Mais bundles RPC | Revisão centralizada entre Go/Gradle/CMake/workflow. Apenas o bundle Linux x64 CPU existente foi efetivamente executado. ARM64/macOS/Windows/Vulkan ainda precisam de build, publicação imutável e hashes revisados. |
| 11 | Releases assinadas | Proveniência existente preservada; inventário CycloneDX e verificação opcional de atestação nos instaladores adicionados. Nova release não foi publicada. Certificado Authenticode e assinatura Android persistente dependem de credenciais próprias. |
| 12 | Distribuição | Dockerfile/Compose Linux adicionados; units systemd existentes preservadas. Docker não disponível para execução local. Pacotes deb/rpm e publicação Homebrew/winget ainda pendentes. |
| 13 | Inglês e contribuição | README.en, guia de operações EN e CONTRIBUTING adicionados. Tradução de todos os guias e internacionalização completa do Android ainda pendentes. |
| 14 | E2E e fuzz | E2E real de pareamento, benchmark TLS, GGUF nativo RPC e SSE executado em Linux CPU. Workflow manual adicionado. Fuzz GGUF e regressões de extração/credenciais/cancelamento executados. Demais plataformas e fuzz de pareamento/extração permanecem complementares. |
| 15 | mDNS | Pendente; descoberta UDP existente mantida. |
| 16 | Múltiplos modelos/prioridade | Fila de workers recebe prioridade estável (-10..10). Host continua com um único runtime/modelo residente; pool de modelos ainda pendente. |
| 17 | KV cache compartilhado | Pendente de suporte/contrato upstream e validação do benefício; nenhuma implementação simulada. |
| 18 | RPC Unix/fd | Pendente de patch upstream. Porta aleatória reduz previsibilidade, mas não autentica RPC nem substitui isolamento. |
| 19 | PAKE auditado | Pendente de revisão criptográfica independente ou migração auditada. Nenhuma certificação externa é alegada. |

## Correções adicionais da revisão

- Credenciais `agent.json` POSIX não podem ser arquivos públicos ou symlinks; diretório privado é endurecido ao gravar.
- Lease revogado é rechecado antes da entrega de trabalho; resultado tardio não acessa nó removido. Revogação/túnel são serializados.
- Requisições desconectadas liberam a fila; cancelamento em execução só libera o nó após confirmação/expiração.
- Resultados grandes demais no worker terminam com erro explícito, evitando lease preso.
- Geração confere o modelo novamente após aguardar a admissão, evitando usar outro modelo depois de unload/load.
- Resposta não-streaming preserva ID, usage e finish_reason do llama-server.
- Bloqueios entre processos protegem staging e publicação sem arquivo de lock permanentemente preso após crash.
- Leitura de corpos HTTP e escrita de frames têm limites de tempo. O relay limpa deadlines herdados do HTTP ao assumir o socket.
- NAT64/Teredo/6to4 são recusados pelo downloader público, e todas as partes GGUF têm seu cabeçalho conferido.
- Limites da fila/histórico, overflow de estimativa e configuração em máquinas com mais de 256 CPUs foram tratados.
- Arquivos Go formatados e `gofmt` obrigatório no PR. CI de PR inclui smoke da CLI/scripts sem disparar build nativo pesado.

## Evidência de execução

- `go test -race ./...`, `go vet ./...`, `git diff --check`, JavaScript syntax/DOM regression e teste do instalador POSIX.
- Cross build da CLI para Linux amd64/arm64/arm, macOS amd64/arm64 e Windows amd64/arm64. Cross build não equivale a execução nesses sistemas.
- `scripts/smoke.py`: HTTPS, cadastro, lease, backend e resposta de chat.
- `scripts/e2e-rpc.py`: pareamento por código, pin TLS, benchmark real, runtime publicado com hash `17d10d9a…`, GGUF de 260K parâmetros com hash `270cba1b…`, carregamento RPC, SSE e rejeição de operação administrativa com chave de inferência.
- Fuzz GGUF curto: 208 execuções sem falha. Isso não substitui fuzz contínuo nem auditoria do parser.
- Validação visual, APK, Flutter, Docker e execução nativa fora do Linux CPU não foram aprovadas nesta sessão.
