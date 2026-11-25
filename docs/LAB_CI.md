# CI do laboratório

O runtime é uma revisão única, sem matriz de compatibilidade RPC antiga. Atualize Host e todos os Guests juntos. Não desative TLS, autenticação, verificação SHA-256 ou assinatura APK.

- PR comum: Go com race/vet e cache, regressão UI; Windows real somente se configuração/toolchain/check mudar. Verificação de patches somente para mudanças nativas.
- PR Android Kotlin/resources: compila UI e testes sem APK nativo; alterações JNI/Rust/Gradle/revisão executam APK debug completo (UI evita repetir Gradle). Main gera APK assinado quando o aplicativo mudar.
- Nova revisão de runtime: dispatch `Portable RPC bundles` **na branch candidata**; revisar os seis builds, testar RPC real e publicar os mesmos artefatos atestados com hashes fixados. Não executar novamente só por atualizar o manifesto. `Published RPC platform validation` e `Native RPC loader PR checks` ficam disponíveis manualmente.
- `Real RPC end-to-end` continua focalizado; dispatch inclui SDK oficial completo e cross-build Windows. A publicação desktop ainda compila todos os alvos em um job Linux e executa testes.
- Medir minutos pela duração dos jobs (com multiplicadores por sistema). Não há estimativa comprovada de economia até comparar runs equivalentes.

## Lote único de desenvolvimento

- Agrupar todas as mudanças P0–P2 da próxima entrega em uma branch e um PR; preparar os commits e realizar verificações locais antes de abrir o PR. Não abrir um PR por item de backlog.
- Se for necessário abrir o PR antecipadamente, mantê-lo em draft: os checks Go, segurança, APK e UI Android não alocam runners nesses PRs. Ao terminar, marcar ready for review uma única vez. Não atualizar a branch enquanto o CI final estiver executando; em caso de correção obrigatória, voltar para draft antes de editar e validar novamente o head final. Não reutilizar checks de outro SHA.
- A decisão de compilar o APK no PR é compartilhada com o workflow de UI. Alterações apenas em hashes/release desktop do manifesto não recompilam Android; mudanças JNI, Rust, Gradle, patches Android ou revisão nativa compilam o APK. Não duplicar a compilação Kotlin/Gradle no check UI quando o APK completo for necessário.
- Builds experimentais nativos continuam sob gatilhos específicos; por isso abrir o PR só depois de concluir o lote. A compilação assinada na main permanece separada da validação debug do PR; nunca disponibilizar o APK debug como atualização pública. Preservar verificações obrigatórias da main e não ignorar falhas.
