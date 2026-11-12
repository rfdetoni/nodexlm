# NodexLM — latência de decode e distribuição RPC

## Diagnóstico de 0,3 tokens/s em um GGUF de ~4 GB

A geração autoregressiva de um token exige execução sequencial das camadas do modelo. Quando pesos e camadas estão divididos entre máquinas, os dados intermediários e comandos GGML atravessam conexões autenticadas por TLS, o relay Go e o RPC nativo. Cada fronteira acrescenta latência, sincronização e processamento. Em Android Wi-Fi, a CPU, o controle térmico e a GPU Vulkan com memória compartilhada também podem ser mais lentos que o Host.

**RPC distribui capacidade de memória; não garante aceleração de tokens/s**. Para um GGUF que cabe integralmente no Host, dividir camadas frequentemente custa mais do que executar tudo localmente.

### Correções introduzidas

1. Para instalações novas e configurações legadas sem estratégia, a estratégia efetiva passa a **Velocidade**. A estratégia **Capacidade** permanece explícita para modelos que exigem RAM agregada e para quem deseja distribuição forçada.
2. Se pesos + reserva e contexto cabem no Host, o algoritmo evita RPC. Só considera outro Guest inteiro, sem dividir camadas, quando benchmarks controlados equivalentes provam vantagem superior a **15%** e o Guest comporta o modelo completo.
3. Medições de conversas distribuídas deixam de se passar por benchmark do Host. São apresentadas separadamente como `@cluster`, `@live-host` e benchmarks `@<node-id>`.
4. O painel agora permite **Medir inferência** escolhendo Host ou um Guest RPC online. As medições usam o mesmo contexto configurado e a mesma carga fixa de 64 tokens. Benchmarks de bases de tempo incompatíveis (decode nativo vs end-to-end) não influenciam decisões.
5. O chat identifica média total incluindo prefill/tempo de primeiro token e, quando disponível, taxa de decode reportada pelo llama.cpp. Caso contrário, a taxa pós-primeiro evento é rotulada **estimativa**, não é uma medição do decoder.
6. O túnel TLS do Guest Android utiliza `TCP_NODELAY` para evitar atrasos do algoritmo de Nagle em comandos/retornos pequenos. No backend Android **Automático** passa a expor somente Vulkan se inicializar, senão somente CPU; a combinação CPU + GPU na mesma RAM vira opção **Híbrido** explícita, reduzindo fronteiras por token.

## Comparação A/B repetível

Use a mesma versão do GGUF, hash, prompt, contexto, `batch_size`, `ubatch_size`, KV quant, configuração de Flash Attention, número de tokens gerados e dispositivo conectado à tomada/estado térmico estável. Descarregue o modelo antes de cada benchmark no painel.

1. Escolha o GGUF de 4 GB; **Medir em: Host local** e execute três rodadas. Registre tokens/s e tipo da taxa (`native_decode` ou `end_to_end`).
2. Para cada Guest online e conectado por RPC, selecione **Medir em: Guest** e repita três vezes. Os resultados de Guests incluem TLS/relay e o backend do dispositivo, não são iguais ao throughput do `llama-cli` diretamente no celular.
3. Repita no Android com backend **CPU**, depois **GPU Vulkan**, reiniciando o serviço RPC entre mudanças. **Automático** escolhe apenas um backend; **Híbrido** combina CPU e GPU apenas sob consentimento.
4. Aplique `Velocidade` e confira **Ver plano**. Para modelo que cabe no Host, deve indicar Host local a menos que um Guest inteiro tenha benchmark comparável mais rápido; force `Capacidade` apenas para investigar uso de memória agregada.
5. Meça separadamente **TTFT** (tempo até o primeiro texto), **decode** e **tempo total**, repetindo prompt com e sem `cache_prompt` em igualdade de condições.
6. Se a geração ainda ficar em ~0,3 t/s, compare a diferença CPU/Vulkan, RTT e MiB/s (botão **Medir rede** por Guest), camadas por dispositivo no plano, `/api/cluster` (`rpc_stats`) e `/api/observability`. Registre se os bytes RPC crescem durante a geração, não apenas no upload inicial.

### Como interpretar

- **Host local significativamente mais rápido:** para esse modelo, deixe Velocidade ou desative distribuição; use o cluster para modelos que não cabem integralmente em uma máquina.
- **Guest CPU mais rápido que Guest Vulkan:** mantenha CPU; o probe de inicialização Vulkan não é um benchmark de desempenho.
- **Guest individual rápido e cluster lento:** suspeite de sincronização por token, multiplicação de fronteiras e RTT, não de taxa de upload dos pesos (upload ocorre no carregamento).
- **TTFT longo mas decode rápido:** a média da conversa pode parecer muito baixa por incluir prefill e primeiro token.
- **Decode nativo já é lento em um Guest:** limite intrínseco do backend ou thermal throttling; aumentar o número de túneis ociosos não melhora a taxa.

## O que esta mudança NÃO faz

Não paraleliza um token autoregressivo entre várias CPUs/GPU de maneira automaticamente mais rápida; não elimina as barreiras da implementação GGML RPC; não garante mais tokens/s por adicionar Androids. A validação em CI cobre compilação e regressões de seleção, não benchmarking físico de Android/Wi-Fi.

A publicação/atualização do APK requer a assinatura de releases persistente descrita na documentação Android.
