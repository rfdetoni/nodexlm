# Mixture of Experts (MoE)

## Capacidades

O NodexLM pode identificar metadados MoE em arquivos GGUF e encaminhar opções do `llama.cpp` para execução local, como `--cpu-moe`, `--n-cpu-moe` e `--moe-cache-mib`, quando o binário oferece suporte.

## Limitações

- O peso total do modelo precisa estar armazenado e os buffers/KV cache também consomem RAM/VRAM.
- A execução RPC distribui tensores e camadas entre dispositivos; isso **não é automaticamente paralelismo por especialista**.
- CPU e Vulkan no Android dependem do modelo, do driver e da memória; Vulkan móvel está em validação experimental.
- Não há garantia de ganho de desempenho ao envolver muitos dispositivos ou redes Wi-Fi lentas.
- O Host não faz escalonamento independente de especialistas MoE nesta versão.

## Próximas validações

Medir tokens por segundo, latência do primeiro token, memória residente, VRAM, temperatura e bateria com modelos densos e MoE. Validar distribuições mistas CPU/Vulkan e limites de memória com dados reais.

Referência: [llama.cpp](https://github.com/ggml-org/llama.cpp).
