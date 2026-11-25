# CI do laboratório

O runtime é uma revisão única, sem matriz de compatibilidade RPC antiga. Atualize Host e todos os Guests juntos. Não desative TLS, autenticação, verificação SHA-256 ou assinatura APK.

- PR comum: Go com race/vet e cache, regressão UI; Windows real somente se configuração/toolchain/check mudar. Verificação de patches somente para mudanças nativas.
- PR Android Kotlin/resources: compila UI e testes sem APK nativo; alterações JNI/Rust/Gradle/revisão executam APK debug completo (UI evita repetir Gradle). Main gera APK assinado quando o aplicativo mudar.
- Nova revisão de runtime: dispatch `Portable RPC bundles` **na branch candidata**; revisar os seis builds, testar RPC real e publicar os mesmos artefatos atestados com hashes fixados. Não executar novamente só por atualizar o manifesto. `Published RPC platform validation` e `Native RPC loader PR checks` ficam disponíveis manualmente.
- `Real RPC end-to-end` continua focalizado; dispatch inclui SDK oficial completo e cross-build Windows. A publicação desktop ainda compila todos os alvos em um job Linux e executa testes.
- Medir minutos pela duração dos jobs (com multiplicadores por sistema). Não há estimativa comprovada de economia até comparar runs equivalentes.
