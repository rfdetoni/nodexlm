# Instalação e atualização do NodexLM

## Requisitos

O Host e os Guests devem estar, preferencialmente, na **mesma LAN**, com acesso a `8443/tcp`. A descoberta automática utiliza `46465/udp`. O painel administrativo só escuta em `localhost:8787`.

A instalação da CLI não exige Go, CMake ou privilégios de administrador. O runtime de inferência `llama.cpp` é preparado quando necessário. O canal padrão é **edge**, destinado a testes.

## Linux e macOS

```sh
curl -fsSL https://raw.githubusercontent.com/rfdetoni/nodexlm/main/install.sh | sh
nodexlm version
```

Arquiteturas: Linux x64, ARM64 e ARMv7; macOS Intel e Apple Silicon. O instalador seleciona o pacote, verifica SHA-256 e instala em `~/.local/bin/nodexlm`. Abra um novo terminal se o comando não aparecer no PATH.

Para uma release estável, **quando existir**:

```sh
curl -fsSL https://raw.githubusercontent.com/rfdetoni/nodexlm/main/install.sh | NODEXLM_CHANNEL=stable sh
```

Para mudar o diretório, configure `NODEXLM_INSTALL_DIR` antes do `sh`. O instalador usa o canal `edge` por padrão.

## Windows

Abra o **PowerShell**:

```powershell
irm https://raw.githubusercontent.com/rfdetoni/nodexlm/main/install.ps1 | iex
nodexlm version
```

Compatível com Windows x64/ARM64. O executável é instalado no perfil do usuário e o PATH é atualizado. Para usar o canal estável quando houver release:

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/rfdetoni/nodexlm/main/install.ps1))) -Channel stable
```

## Android (sem Play Store)

1. Abra a [release pública do APK de testes](https://github.com/rfdetoni/nodexlm/releases/tag/android-debug).
2. Baixe `NodexLM-android10-plus-debug.apk` e autorize a instalação pelo navegador/gerenciador de arquivos, se solicitado.
3. Use Android **10 (API 29) ou superior** e CPU ARMv7 ou ARM64.
4. Para o Guest RPC, pareie com o Host e selecione o backend. O ARM64 tenta Vulkan se o driver for compatível; o ARMv7 usa CPU.
5. Permita a execução em segundo plano conforme as opções de bateria do dispositivo.

**Assinatura:** builds debug do CI podem usar certificados diferentes. Atualização por cima de um APK com outra assinatura falha; desinstalar e reinstalar pode apagar dados locais. Releases assinadas exigem uma chave persistente própria. Não há instalação silenciosa para apps comuns.

## Host e Guest

```sh
# Computador principal
nodexlm host

# Outro computador Linux
nodexlm guest
# ou para somente CPU
nodexlm guest --backend cpu
```

No Host, use `nodexlm web-token` para obter a senha do painel. O código temporário de 8 caracteres é exclusivo do **pareamento**, não do painel.

## Runtime RPC Linux

No Linux x64/CPU, o NodexLM procura o runtime `llama.cpp b12143` pré-compilado e verificado; para outros sistemas ou backends, pode ser necessário compilar ou fornecer um binário compatível.

Os executáveis podem depender de bibliotecas GGML (`libggml.so.0`, etc.) armazenadas ao lado do runtime. O NodexLM as inclui no caminho de bibliotecas ao iniciar o processo, sem exigir instalação global.

Se o runtime estiver incompleto, consulte o [guia de diagnóstico](docs/GUIA_DIAGNOSTICO.md). Não apague `~/.nodexlm/agent.json` apenas para corrigir uma dependência nativa: ali ficam as credenciais de pareamento.

## Atualização

Execute novamente o mesmo comando de instalação, encerre os processos antigos e reinicie `nodexlm host` ou `nodexlm guest`. Os arquivos de configuração privados permanecem no diretório de dados.

Veja também [Host](docs/GUIA_HOST.md), [Guest Linux](docs/GUIA_GUEST_LINUX.md) e [Android](docs/GUIA_ANDROID.md).
