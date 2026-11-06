# Pareamento e segurança

O NodexLM oferece ingresso de dispositivos na **mesma rede local**, sem usar o token administrativo como credencial de Guest.

## Fluxo de pareamento

1. Execute `nodexlm host`. O Host cria um código temporário de 8 caracteres, válido por cerca de 10 minutos.
2. Em outra máquina execute `nodexlm guest`; no Android use **Cluster → Conectar com segurança**.
3. O Guest identifica o Host via UDP `46465` e verifica o vínculo do código com o certificado TLS.
4. Após o pareamento, recebe ID e credencial próprios, utilizados no HTTPS `8443` e nos túneis RPC.
5. Nas próximas conexões, as credenciais são reutilizadas sem informar o código novamente.

O código temporário **não** substitui `nodexlm web-token`, que é o acesso administrativo ao painel `localhost:8787`.

## Rede sem descoberta automática

```sh
nodexlm guest --server https://192.168.1.100:8443
```

No Android, informe o endereço HTTPS do Host e o mesmo código de pareamento.

## Revogação

No Host:

```sh
nodexlm nodes
nodexlm revoke --id ID_DO_GUEST
nodexlm pairing
```

O comando `pairing` gera um novo código temporário e não substitui as credenciais dos Guests já registrados.

## Limites e recomendações

O esquema combina código aleatório, compromisso criptográfico PBKDF2, TLS e verificação/fixação do certificado. **Não é um PAKE padronizado auditado** (por exemplo, SPAKE2+ ou OPAQUE). Ele deve permanecer restrito à LAN confiável; não exponha a porta do coordenador à Internet sem revisão de segurança.

Proteja o conteúdo de `~/.nodexlm` e não publique tokens, certificados ou logs com credenciais. Para acessar o painel de outra máquina, prefira um túnel SSH ao invés de abrir a interface administrativa na rede.

Os servidores RPC nativos escutam no loopback dos Guests; o tráfego do cluster passa por túneis TLS autenticados. Para detalhes operacionais, veja [Host](GUIA_HOST.md) e [Guest Linux](GUIA_GUEST_LINUX.md).
