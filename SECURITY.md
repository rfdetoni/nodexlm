# Política de segurança do NodexLM

O NodexLM ainda é **experimental (alpha)**. Os dispositivos Host e Guest
devem operar em uma LAN ou VPN confiável; não exponha a porta HTTPS do Host
diretamente à Internet.

## Reportar vulnerabilidades

Não divulgue credenciais, código de exploração ou dados de dispositivos em
issues públicas. Prefira o recurso **Report a vulnerability** na aba
Security do repositório, quando disponível:

https://github.com/rfdetoni/nodexlm/security/advisories/new

Se o recurso não estiver habilitado, abra uma issue **sem informações
sensíveis** solicitando um canal privado de contato.

## Limites conhecidos

- O protocolo nativo GGML RPC não autentica conexões na interface de
  loopback. Não execute Guests em máquinas compartilhadas ou não confiáveis.
- As credenciais de pareamento e o arquivo `secrets.json` devem ficar
  restritos à conta que executa o Host.
- Um certificado TLS já pareado é fixado no Guest; sua substituição
  requer novo pareamento até existir um fluxo de rotação autenticado.
- Evite distribuir APKs debug em dispositivos que contenham dados sensíveis.

Inclua no relato a versão (`nodexlm version`), sistema operacional e
passos mínimos para reprodução, **sem enviar tokens, chaves ou credenciais**.
