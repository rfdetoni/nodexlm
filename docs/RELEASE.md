# Assinatura, distribuição e aceite de release

A implementação de assinatura e distribuição está na `main`. Uma release de produção depende de identidade de assinatura configurada, teste físico e avaliação criptográfica independente. O preview imutável não recebe o rótulo de release estável nem substitui esses aceites.

## Identidade Android permanente

Foi preparada uma identidade PKCS#12 RSA-4096, separada das chaves debug/CI. O fingerprint público SHA-256 é:

```
1aba7de39fb9d6147eed95abd3de9092058301359b27b37b8bd147984da421ff
```

O backup cifrado e a senha são entregues separadamente ao proprietário. Nenhuma chave privada ou senha foi adicionada ao repositório. Preserve essa mesma identidade em futuras versões: gerar outra chave altera a identidade do app instalado.

Para configurar a identidade já entregue:

1. Extraia `nodexlm-android-signing-backup.zip` em um diretório privado fora do Git.
2. Coloque a senha recebida separadamente como `password.txt` nesse diretório; no POSIX, use `chmod 600 password.txt`.
3. Com `keytool`, Python 3 e GitHub CLI instalados, autentique o GitHub CLI na conta com acesso de administrador ao repositório.
4. Nesse diretório, execute:

```sh
python3 android-signing.py configure --directory . --repository rfdetoni/nodexlm
```

O script verifica o certificado e envia os quatro secrets por stdin, sem senha nos argumentos: `ANDROID_KEYSTORE_BASE64`, `ANDROID_KEYSTORE_PASSWORD`, `ANDROID_KEY_ALIAS`, `ANDROID_KEY_PASSWORD`. Ele recusa sobrescrever secrets existentes. Em uma falha parcial, informa somente os nomes instalados; retenha o mesmo backup e complete a configuração com essa identidade, sem gerar uma chave nova.

Para criar uma identidade em outra instalação, o script do repositório oferece `generate --directory CAMINHO`. Ele exige um diretório novo, fora de repositórios Git. Isso é bootstrap de uma identidade nova, não um procedimento de rotação.

O workflow Android verifica um build release minificado mesmo sem secrets, usando uma chave efêmera de teste. Esse APK não é publicado. Releases reais usam a identidade persistente configurada. A transição de APK debug para release pode exigir reinstalação; preserve os dados antes.

O checksum público Android usa `SHA256SUMS-android.txt`. Ele não sobrescreve `SHA256SUMS.txt` dos arquivos desktop/CLI.

## Authenticode Windows

O pipeline aceita um PKCS#12/PFX de certificado Code Signing confiável com chave privada exportável, através de:

| Secret | Conteúdo |
|---|---|
| `WINDOWS_PFX_BASE64` | PFX cifrado, codificado em base64 |
| `WINDOWS_PFX_PASSWORD` | Senha desse PFX |

O proprietário deve obter o certificado confiável de sua autoridade/provedor. O projeto não gera um certificado autoassinado e o apresenta como assinatura de produção. Certificados cujo provedor exige HSM/assinatura remota precisam de integração específica do provedor; não suponha que a chave seja exportável para PFX.

Após configurar os dois secrets, o pipeline:

1. Compila os destinos e transfere os artefatos ao runner Windows.
2. Importa a identidade apenas no usuário temporário do runner.
3. Assina os executáveis x64/ARM64 com SHA-256 e timestamp RFC 3161, verifica cadeia/identidade e rejeita entradas cujo checksum mudou.
4. Recalcula os checksums dos ZIPs assinados, atualiza o SBOM e regenera manifests estáveis.
5. Atesta os arquivos finais e só então publica. As chaves importadas são removidas ao terminar.

Tags estáveis `vX.Y.Z` falham se a assinatura Windows não estiver configurada. O canal `edge` permanece experimental e pode distribuir executáveis sem Authenticode. Os testes de assinatura usam certificado descartável no runner e nunca publicam esse certificado como identidade do produto. A [validação real x64/ARM64 passou](https://github.com/rfdetoni/nodexlm/actions/runs/42400138899), incluindo timestamp RFC 3161, rejeição de binário/arquivo adulterado e atualização de metadados. O endpoint de timestamp padrão é o RFC 3161 DigiCert por HTTP; a assinatura e a cadeia do timestamp são verificadas pelo SignTool.

## Homebrew e WinGet

O [preview imutável](https://github.com/rfdetoni/nodexlm/releases/tag/nodexlm-preview-274ef979) congela os artefatos do build `274ef979`, com checksums, SBOM e manifests. Ele evita que uma atualização de `edge` invalide um hash já publicado.

A fórmula do próprio repositório pode ser instalada sem depender da aceitação no índice central:

```sh
brew tap rfdetoni/nodexlm https://github.com/rfdetoni/nodexlm
brew install rfdetoni/nodexlm/nodexlm
```

Ela instala o preview fixado, não uma versão estável de produção. O teste de Homebrew instala a fórmula e executa `brew test`.

Os três manifests do WinGet estão em `distribution/winget-preview`. A validação usa os schemas oficiais Microsoft 1.6.0 fixados por commit e confere os SHA-256 dos instaladores publicados. Se o WinGet local estiver configurado para aceitar manifests locais:

```powershell
winget validate --manifest .\distribution\winget-preview
winget install --manifest .\distribution\winget-preview
```

Isso não afirma que `winget install rfdetoni.NodexLM` já esteja disponível no índice público. A submissão ao repositório Microsoft deve usar a versão estável aprovada e sua pasta `manifests/r/rfdetoni/NodexLM/X.Y.Z`; ela depende da revisão dos mantenedores. Homebrew core também exige revisão externa; o tap próprio já atende a instalação sem essa dependência.

## Aceite físico reproduzível

Com o Host em execução, modelos residentes descarregados e um GGUF disponível:

```sh
python3 scripts/hardware-acceptance.py --model MODELO.gguf --node ID_DO_GUEST --repeat 10 --output acceptance-results/android-cpu.json
```

Sem `--node`, a medição é do Host. O token é lido de `NODEXLM_ADMIN_TOKEN` ou de `nodexlm web-token`, sem entrar no relatório ou nos argumentos. `--host` e `--ca` atendem um gerenciamento HTTPS configurado; fora do loopback, HTTP é recusado. O script não desaloja modelos nem sobrescreve evidências anteriores. O relatório é gravado com permissão 0600.

O coletor aguarda a reconexão de um túnel ocioso entre as cargas e mede rede/inferência reais. O campo `physical_acceptance` permanece `not-established`: um JSON gerado em CI/VM não comprova hardware físico.

Para aceite, registre em aparelhos reais:

| Cenário | Evidência necessária |
|---|---|
| Android CPU/Vulkan | Modelo, aparelho, SO/ROM, driver/backend efetivo e medições |
| Tela apagada por 30 minutos | Serviço continua ativo, geração/reconexão funcionam |
| Energia | Restrição de carga, limiar de bateria e janela de horário são respeitados |
| Térmico | Parada do serviço em condição térmica observada, sem forçar superaquecimento |
| Queda e retorno da conexão | Reconexão, nova geração e ausência de processo nativo abandonado |
| Metal Apple físico | Execução GPU e medições no hardware real; CPU em VM não atende esse aceite |
| Windows/Linux | Instalação, reinício, cancelamento e inferência no sistema alvo |

Os relatórios podem conter nomes/IDs dos dispositivos. Revise esses dados antes de publicá-los. Não inclua tokens, prompts privados ou chaves nos relatórios.

## Auditoria do pareamento

O KDF Go foi substituído pela biblioteca padrão, mantendo o vetor compatível com Android. CLI aceita `--fingerprint SHA256` obtido fora da rede; Android respeita o certificado importado/já pareado e limita leituras de entrada. O protocolo por código continua customizado, com prova pública que não é um PAKE auditado.

A revisão independente deve cobrir `internal/pairing`, endpoints do coordenador, `join/guest`, `PairingClient`, TLS fixado, proteção das credenciais e revogação. Valide ataques offline ao código de baixa entropia, replay, expiração, troca de certificado, limite de tentativas, downgrade e rollout Go/Android. Os vetores e E2E estão no repositório. Trocar para outra biblioteca sem auditoria não fecha esse requisito.

Uma auditoria só pode ser registrada como aprovada com relatório real de avaliador independente, escopo/commit e correções revalidadas. Até lá, o modelo de confiança continua LAN/VPN.
