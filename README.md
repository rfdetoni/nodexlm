# NodexLM

[English](README.en.md) · [Operação e API](docs/OPERATIONS.md)

**Cluster local de inferência com GGUF, Host, Guests e distribuição de camadas por RPC.**

O NodexLM usa o [llama.cpp](https://github.com/ggml-org/llama.cpp) para executar modelos de linguagem em uma rede local. Um Host coordena os dispositivos; Guests Linux, Windows, macOS e Android podem disponibilizar recursos computacionais, conforme a compatibilidade do backend.

> **Estado: experimental (alpha).** A compilação não comprova inferência distribuída em qualquer combinação de aparelhos. Desempenho, memória disponível, drivers e estabilidade precisam ser medidos em hardware real.

## Instalação rápida

**Linux e macOS** (CLI e painel Web integrado):

```sh
curl -fsSL https://raw.githubusercontent.com/rfdetoni/nodexlm/main/install.sh | sh
nodexlm host
```

**Windows PowerShell** (CLI e painel Web integrado):

```powershell
irm https://raw.githubusercontent.com/rfdetoni/nodexlm/main/install.ps1 | iex
nodexlm host
```

**Android 10+**: [APK experimental no GitHub Releases](https://github.com/rfdetoni/nodexlm/releases/tag/android-debug). O Android ARM64 pode tentar Vulkan + CPU; ARMv7 permanece CPU. A GPU móvel depende do driver e de suporte Vulkan efetivo. A versão debug não equivale a uma release assinada para produção.

O instalador de desktop distribui **a CLI e o painel Web**, não a interface Flutter opcional.

## Começando

1. Inicie `nodexlm host` no computador principal.
2. Acesse **http://localhost:8787** e obtenha o acesso com `nodexlm web-token` em outro terminal do Host.
3. Execute `nodexlm guest` no Linux secundário; informe o código de pareamento temporário que o Host exibiu.
4. No Android, abra **Cluster**, faça o pareamento e ative **Guest RPC** com o backend adequado.
5. No painel Web, pesquise ou importe um GGUF, habilite distribuição por RPC quando houver Guests prontos e carregue o modelo.

O código de pareamento **não** é o token de administrador. O GGUF de um modelo distribuído fica no Host; o protocolo RPC movimenta tensores e operações entre dispositivos.

## Documentação em português

- [Instalação e atualização](INSTALL.md)
- [Guia do Host e painel Web](docs/GUIA_HOST.md)
- [Guia do Guest Linux](docs/GUIA_GUEST_LINUX.md)
- [Guia do Android: CPU e GPU](docs/GUIA_ANDROID.md)
- [Modelos GGUF e Hugging Face](docs/GUIA_MODELOS.md)
- [Diagnóstico e solução de problemas](docs/GUIA_DIAGNOSTICO.md)
- [Pareamento e segurança](docs/PAIRING.md)
- [Model Hub: detalhes técnicos](docs/MODEL_HUB.md)
- [MoE e limitações](docs/MOE.md)
- [Histórico de alterações](CHANGELOG.md)

O painel oferece chat incremental com histórico apagável, plano de camadas/KV, métricas e medições reais de rede/inferência. Até quatro modelos podem permanecer residentes: o principal pode usar RPC; os adicionais usam o Host, pois cada Guest nativo atende uma conexão de modelo de cada vez.

Consulte o [estado dos 19 itens do roadmap](docs/ROADMAP_STATUS.md) para evidências e dependências externas.

## Rede e limitações

| Serviço | Porta | Acesso |
|---|---|---|
| Painel Web | `127.0.0.1:8787/tcp` | Apenas no Host local |
| Coordenador HTTPS | `8443/tcp` | Dispositivos pareados na LAN |
| Descoberta de Host | `46465/udp` | Rede local |
| Descoberta DNS-SD | `5353/udp` | `nodexlm.local` / `_nodexlm._tcp.local`, quando disponível |
| RPC nativo | Socket Unix privado; TCP loopback em Windows/legado | Relay TLS autenticado; acesso local limitado pela conta |

**RAM e VRAM não são simplesmente somadas.** GPUs integradas normalmente compartilham memória com o sistema, e os cálculos de compatibilidade são estimativas. Android pode interromper serviços em segundo plano conforme o fabricante. A distribuição RPC não representa paralelismo de especialistas MoE automático.

Código-fonte licenciado sob **AGPL-3.0-only**. Projeto independente; não utiliza código do aplicativo PrivateLM.

## Verificar a procedência dos binários

Os arquivos desktop/CLI da release `nodexlm-edge` possuem um atestado
criptográfico de build associado ao commit e ao workflow do GitHub Actions.
Após baixar o arquivo, verifique o SHA-256 do release e o atestado com o
GitHub CLI (não é necessário confiar só no checksum publicado junto do binário):

```sh
gh attestation verify nodexlm_linux_amd64.tar.gz --repo rfdetoni/nodexlm
```

As releases experimentais Android não são equivalentes a uma assinatura
persistente de produção; um APK debug novo pode exigir reinstalação.

