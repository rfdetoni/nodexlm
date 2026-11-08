# Estado do roadmap — 07/11/2026

Os 19 itens estão mapeados abaixo. As funcionalidades foram implementadas e validadas conforme a evidência indicada. **O roadmap não pode ser declarado 100% aceito enquanto faltarem auditoria criptográfica independente, credenciais de assinatura de produção e validações em hardware físico.** Compilação, teste automatizado e auditoria externa são evidências diferentes.

| # | Item | Entrega e aceitação |
|---|---|---|
| 1 | GGUF em shards | Host/CLI e Android agrupam conjuntos completos, somam tamanhos, importam todas as partes e publicam a primeira somente após completar o conjunto. Android usa seleção múltipla e fila persistida de downloads. Testes Go/JUnit e build APK passaram. |
| 2 | Download robusto | Downloader Go compartilhado: Range/If-Range, SHA-256 LFS, espaço disponível, timeout de inatividade, parciais persistentes, publicação exclusiva e bloqueio entre processos. Android utiliza DownloadManager e verifica cabeçalho/tamanho/SHA quando o metadado existe. As garantias de retomada do sistema Android não são equiparadas às do Go. |
| 3 | API OpenAI SSE | Host nativo, worker Go e Android independente transmitem texto incremental e cancelam a execução. Amostragem, stop, UTF-8, usage e chave de inferência restrita. E2E nativo passou; APK/JNI compilado. O Host agora preserva ferramentas, JSON Schema e Responses, com aceitação SDK/Agents em modelo real; workers independentes mantêm texto. Veja [compatibilidade OpenAI](OPENAI_COMPATIBILITY.md). Imagens/áudio continuam fora do runtime gerenciado. |
| 4 | doctor | Texto/JSON, listeners locais, permissões, disco, certificado, CPU/libc/Vulkan e estado do cluster. Firewall remoto/SAN/rede são apresentados com seus limites; sem afirmar conectividade não medida. |
| 5 | Chat do painel | Múltiplos turnos, histórico local confirmado/apagável, parâmetros, SSE, interrupção e tempos medidos. Browser E2E real passou em 1280×900 e 390×844; corrigidos restauração do histórico e overflow móvel. Capturas revisadas. |
| 6 | Plano do cluster | Camadas/pesos por nó, KV F16 estimado com metadados de arquitetura, medições de RTT/banda e tokens/s observados por modelo/nó. Não converte eco em previsão de tokens/s. Mudanças de configuração podem invalidar comparações. |
| 7 | Observabilidade | Contadores de geração/erros/tokens reportados, histograma cumulativo de duração, gauges de throughput/cache, reconexões e capacidade de túneis. Histórico RTT limitado e gráfico no painel. Tokens ausentes de usage/interrupções não são inventados. |
| 8 | bench | CLI/UI medem RTT/eco no túnel TLS e executam carga real de inferência no Host ou em um Guest escolhido. Resultados alimentam o plano. Exige descarregar modelos antes da carga; não desaloja modelos silenciosamente. E2E real passou. |
| 9 | Energia Android | Carga, bateria mínima, horário noturno, térmico, notificação e parada deliberada. Regressões JUnit passaram; APK universal CPU ARMv7/CPU+Vulkan ARM64 compilado. Consumo/temperatura/ROM exigem aparelho físico. |
| 10 | Mais bundles RPC | Cinco builds aprovados: Linux amd64 CPU/Vulkan, Linux arm64 CPU, macOS arm64 Metal e Windows amd64 CPU. [Release r3 publicada](https://github.com/rfdetoni/nodexlm/releases/tag/nodexlm-rpc-b12143-r3), com cinco hashes SHA-256 conferidos entre manifesto e metadados dos assets antes de ativá-los no código. O E2E Unix nativo Linux passou no build. [A validação dos cinco bundles publicados passou](https://github.com/rfdetoni/nodexlm/actions/runs/42370326838), com CPU explícito no macOS paravirtual. |
| 11 | Releases assinadas | Atestações GitHub/SLSA, inventário CycloneDX com módulos Go e pacotes, verificação opcional de procedência nos instaladores e fluxo Android de assinatura persistente em tags. Identidade Android permanente RSA-4096 gerada com backup cifrado entregue separadamente da senha; instalação dos secrets ainda exige autenticação do proprietário. Pipeline Authenticode x64/ARM64, timestamp, verificação e regeneração de hashes/SBOM implementado; certificado Windows confiável continua externo. O teste de release Android usa chave efêmera sem publicá-la. |
| 12 | Distribuição | CLI/Web em sete destinos, pacotes deb/rpm amd64/arm64/armhf, contas/units systemd e Docker/Compose Linux. Build/empacotamento desktop passou. Fórmula Homebrew/manifests winget são gerados de checksums reais em tags estáveis; tap Homebrew próprio e manifests WinGet preview foram adicionados com artefatos imutáveis. Aprovação nos índices centrais permanece externa; não é necessária para usar o tap próprio. [Docker validado em CI](https://github.com/rfdetoni/nodexlm/actions/runs/42369733864): Compose, imagem, Host sem root com filesystem somente leitura, API autenticada e identidade persistida no reinício. |
| 13 | Inglês e contribuição | README EN, operações, guias Host/Guest/Android/modelos/diagnóstico/pareamento, CONTRIBUTING e recursos Android EN/pt-BR. Erros de sistema/runtime podem permanecer no idioma original. Frontend Flutter opcional corrigido e aprovado por `flutter analyze`. |
| 14 | E2E e fuzz | E2E real TLS/RPC, pool, benchmark, SSE, chave restrita, navegador e mDNS em CI. Fuzz GGUF, pareamento e caminhos de extração. Testes/cross-build e análise de vulnerabilidades Go passaram. Não equivale a teste em todos os aparelhos/drivers. |
| 15 | mDNS | DNS-SD `_nodexlm._tcp.local` e `nodexlm.local`, anúncios, conflito, TTL de saída e fallback UDP. Descoberta multicast real passou em CI Linux. Limitado a redes privadas IPv4 com multicast disponível; identidade ainda exige código/certificado. |
| 16 | Múltiplos modelos/prioridade | Até quatro modelos residentes com orçamento de memória, processos/KV independentes, listagem e unload individual; prioridade estável -10..10 na fila. Principal pode usar RPC; adicionais usam o Host porque o GGML fixado atende um cliente por Guest. Pool e geração direcionada passaram em E2E. |
| 17 | KV cache compartilhado | Reutilização de prefixo via `cache_prompt`, KV nos backends que executam as camadas e métricas de tokens reutilizados do runtime fixado. Estado isolado por modelo/processo. Não há compartilhamento genérico de tensores KV entre modelos ou sessões incompatíveis; isso exige outro contrato upstream. |
| 18 | RPC Unix/fd | Patch real de `llama.cpp b12143`, build embarcado Android, Host/Guest POSIX por sockets privados e seleção explícita/automática de transporte. E2E Unix Linux passou no CI. Windows e runtime customizado sem suporte ficam em TCP; nenhum suporte fd inexistente é anunciado. |
| 19 | PAKE auditado | **Bloqueado por avaliação externa.** O protocolo atual por código/certificado é customizado e não recebe o nome de PAKE auditado. Requer auditoria criptográfica independente ou migração interoperável revisada por especialista, incluindo Go/Android, vetores e rollout. KDF Go usa a biblioteca padrão; fingerprint completo opcional na CLI, certificado conhecido/importado respeitado no Android e leituras limitadas foram implementados. A revisão independente continua pendente. |

## Evidências reproduzíveis

- [Validação final do runtime e navegador](https://github.com/rfdetoni/nodexlm/actions/runs/42370633600) aprovada após retirar a credencial dos argumentos nativos. [Build final desktop/CLI](https://github.com/rfdetoni/nodexlm/actions/runs/42370633605) aprovado com regressão da credencial, testes Go/vet, cross-compilação e pacotes.

- Go: `go test -race ./... -timeout 90s`, `go vet ./...`, `govulncheck ./...` (nenhuma vulnerabilidade encontrada na execução), regressão DOM do catálogo e smoke do instalador.
- A credencial da API nativa é transmitida por ambiente, sem aparecer nos argumentos do processo; regressão cobre sobrescrita de credencial herdada e ausência nos argumentos.
- Native TCP local: `NODEXLM_TEST_RPC_TRANSPORT=tcp NODEXLM_RPC_DIR=/tmp/nodexlm-native-build NODEXLM_TEST_MODEL=/tmp/nodexlm-tiny.gguf python3 scripts/e2e-rpc.py`. Inclui dois modelos residentes, geração no segundo modelo e benchmark em um Guest real.
- [CI nativo de cinco plataformas](https://github.com/rfdetoni/nodexlm/actions/runs/42368564577): os cinco jobs de build passaram, incluindo inferência Unix Linux. A primeira tentativa de publicação recebeu HTTP 403 do GitHub; [a publicação corrigida passou](https://github.com/rfdetoni/nodexlm/actions/runs/42369384350), reutilizando os artefatos com conferência de identidade do build/checksums.
- [E2E navegador e multicast](https://github.com/rfdetoni/nodexlm/actions/runs/42368732058): login, duas gerações, persistência/deleção do histórico, plano, métricas, interrupção, layouts desktop/mobile e DNS-SD real aprovados. Capturas disponíveis no artifact desse run.
- [Docker](https://github.com/rfdetoni/nodexlm/actions/runs/42369733864) aprovado com ciclo de vida/reinício e configuração restrita.
- [Análise Flutter](https://github.com/rfdetoni/nodexlm/actions/runs/42369160094) aprovada.
- [Bundles publicados em cinco plataformas](https://github.com/rfdetoni/nodexlm/actions/runs/42370326838): pareamento TLS, inferência RPC, pool, benchmark e API restrita aprovados. macOS usa CPU por limitação da GPU virtual; Linux Vulkan usa driver de CI.
- [APK e JUnit](https://github.com/rfdetoni/nodexlm/actions/runs/42368522518) e [pacotes desktop/CLI](https://github.com/rfdetoni/nodexlm/actions/runs/42368732076) aprovados.
- Fuzz curto: 71.481 entradas de descoberta/pareamento e 15.510 caminhos de arquivo sem falha. Há corpus GGUF adicional; esses tempos curtos não substituem fuzz contínuo/auditoria.
- Upstream nativo: `2bc46baa9207d098d03741ac394e7faaf1a955b6`; modelo de teste: `stories260K.gguf`, SHA-256 `270cba1bd5109f42d03350f60406024560464db173c0e387d91f0426d3bd256d`.

## Dependências externas para aceite final

1. Contratar/executar revisão criptográfica independente do pareamento ou aprovar uma migração PAKE interoperável auditada. Até lá, permanece o modelo de confiança LAN/VPN documentado.
2. Instalar os quatro secrets da identidade Android já preparada e fornecer um certificado Windows Code Signing confiável quando houver release estável. Atestação de procedência não é Authenticode nem certificado de auditoria.
3. Validar Android/Metal/Vulkan em aparelhos e drivers reais, energia/térmico em ROMs relevantes no ambiente alvo. O container foi validado no CI Linux; Docker Desktop e redes diferentes ainda exigem teste no destino. O CI com GPU virtual/software não comprova desempenho físico. A GPU paravirtual macOS não suporta RMS_NORM; o bundle Metal é exercitado com fallback CPU explícito, enquanto a aceleração exige hardware Apple compatível.
4. Criar uma tag estável após o aceite e submeter os manifests aos índices centrais. Tap próprio e preview WinGet imutável já estão disponíveis. A aceitação desses projetos depende de seus mantenedores.

Os contratos de texto OpenAI, pool e RPC estão descritos nos guias. Nenhum checkbox altera os limites efetivos do GGML ou certifica uma avaliação externa que não ocorreu.

## Complementos de 07/11 — fechamento da preparação de release

- [Guia executável de assinatura e aceite](RELEASE.md): chave Android, secrets, Authenticode, tap/preview e coleta em aparelhos.
- Snapshot imutável `nodexlm-preview-274ef979` publicado com artefatos do build aprovado `42398200821`, sem promover a versão a produção.
- Coletor `scripts/hardware-acceptance.py` mede de fato e mantém `physical_acceptance=not-established`; teste de CI não se passa por teste físico.
- Colisão entre `SHA256SUMS.txt` Android/desktop corrigida usando `SHA256SUMS-android.txt`.
- Scripts de assinatura não incluem credenciais no Git. Configuração de secrets depende da conta autenticada do proprietário; esta sessão não possui esse acesso administrativo.

### Validação dos complementos

| Verificação | Evidência |
|---|---|
| APK debug, bibliotecas nativas, JUnit e release minificada com identidade de teste | [Android aprovado](https://github.com/rfdetoni/nodexlm/actions/runs/42398200940) |
| Instalação real pelo tap e `brew test` | [Homebrew aprovado](https://github.com/rfdetoni/nodexlm/actions/runs/42399443870) |
| Schemas oficiais WinGet, hashes dos downloads e regressões de manifests | [Registry aprovado](https://github.com/rfdetoni/nodexlm/actions/runs/42399741601) |
| Cinco bundles RPC publicados e coletor de medições | [RPC aprovado](https://github.com/rfdetoni/nodexlm/actions/runs/42399443830) |
| Pareamento com rejeição de fingerprint errado, RPC real e navegador | [E2E aprovado](https://github.com/rfdetoni/nodexlm/actions/runs/42398983230) |
| Assinatura Authenticode x64/ARM64, timestamp RFC 3161, rejeição de adulteração e renovação dos hashes/SBOM (identidade descartável de CI) | [Assinatura aprovada](https://github.com/rfdetoni/nodexlm/actions/runs/42400138899) |
| Snapshot imutável e publicação por digest | [Distribuição aprovada](https://github.com/rfdetoni/nodexlm/actions/runs/42398983229) |

Esses testes não instalaram secrets de produção, não executaram uma auditoria externa nem comprovaram comportamento em aparelhos físicos.
