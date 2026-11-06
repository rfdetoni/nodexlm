# Guia do Android: Guest RPC com CPU e GPU

## Compatibilidade

- **Android 10+** com arquitetura ARMv7: Guest RPC **CPU**.
- **Android 10+** com arquitetura ARM64: Guest RPC **CPU** e tentativa de backend **Vulkan**.
- O aparelho precisa fornecer GPU Vulkan e driver compatível com o backend do `llama.cpp b12143` para usar GPU.
- GPUs móveis frequentemente compartilham memória com a CPU. **Não some RAM e VRAM como se fossem capacidades independentes.**

O app é experimental; build aprovado não comprova execução ou desempenho em todos os celulares.

## Instalar e conectar

1. [Baixe o APK de testes](https://github.com/rfdetoni/nodexlm/releases/tag/android-debug).
2. No Linux, execute `nodexlm host`.
3. No Android, abra **Cluster → Conectar ao Host** e informe o código de pareamento temporário.
4. Em **Backend de processamento**, escolha o modo apropriado e pressione **Ativar Guest RPC (sem GGUF local)**.
5. Na notificação do Android, verifique se o Guest permanece ativo. Para interromper, use **Parar Guest RPC**.

O servidor GGML RPC é iniciado em um processo Android isolado, na porta **127.0.0.1:50052**, e só recebe chamadas do Host por túneis HTTPS autenticados. O GGUF permanece no Host, e o Android recebe buffers, tensores e grafos.

## Modos

| Opção | Comportamento |
|---|---|
| **Automático** | Tenta GPU Vulkan + CPU; se Vulkan não existir, usa CPU |
| **Somente CPU** | Mantém o processamento na CPU |
| **Somente GPU Vulkan** | Exige que uma GPU Vulkan funcional seja detectada |
| **GPU Vulkan + CPU** | Exige Vulkan e oferece ambos os backends ao Host |

Em aparelhos ARM32 a GPU não está incluída no APK atual: utilize **Automático** ou **Somente CPU**.

## RAM, VRAM e bateria

O heartbeat informa RAM e, quando o driver Vulkan expõe valores, informações de memória GPU. Em GPUs integradas, esse espaço pode ser compartilhado; a indicação é informativa e não garante orçamento adicional.

O Android utiliza um serviço em primeiro plano, wake lock de CPU e bloqueio de economia de energia do Wi-Fi durante a atividade. Isso aumenta o consumo e aquece o aparelho. Algumas ROMs ainda interrompem serviços bloqueados; habilite operação sem restrição nas configurações de bateria se necessário.

## Quando a GPU não aparece

- Verifique se o dispositivo usa **ARM64**.
- Escolha **Automático** e observe se a interface detecta a GPU.
- Em aparelhos sem Vulkan funcional, o CPU é o fallback correto.
- Com ADB conectado, use `adb logcat -s NodexLM-RpcGuest:* NodexLM-RPC:* AndroidRuntime:E`.

A **inferência independente** continua existindo para GGUF armazenado no telefone, mas não compartilha camadas do modelo do Host.

Veja [Host](GUIA_HOST.md) e [diagnóstico](GUIA_DIAGNOSTICO.md).
