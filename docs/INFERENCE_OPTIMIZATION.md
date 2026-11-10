# Motor de inferência: estratégias e parâmetros experimentais

O Host e o escalonador continuam em Go. O llama.cpp/GGML permanece responsável
por kernels, alocação real de camadas, KV cache e execução das operações.

## Estratégias

Na tela **Configuração de inferência**, habilite a distribuição e escolha:

- **Capacidade (padrão):** mantém a compatibilidade com o comportamento original:
  utiliza todos os Guests RPC prontos e divide camadas conforme memória estimada.
- **Velocidade:** ordena Guests com base no benchmark *real* do mesmo GGUF
  (quando disponível), RTT e taxa de eco da rede, e usa a menor combinação
  que satisfaz a margem estimada de memória. Guests com links ruins ficam
  atrás, mas continuam elegíveis quando necessários ao modelo.

Use **Medir rede** em cada Guest. Para medir tokens/s individuais, descarregue
modelos e execute o benchmark real selecionando aquele Guest. Métricas com mais
de 24 horas expiram; ausência de medição não significa que o nó seja lento.

A seleção é executada **antes** do carregamento e reutilizada no endpoint de
prévia `GET /api/plan`. Ao mudar a conectividade entre a prévia e a
aplicação, os resultados podem diferir. A divisão calculada é estimativa,
pois a distribuição final é determinada pelo GGML. Não há troca de camadas
a cada token, nem previsão de tokens/s a partir de RTT/banda.

## Controles nativos opcionais

O formulário expõe os seguintes campos, por padrão sem modificar as opções
do llama-server:

| Campo | Valores | Consideração |
| --- | --- | --- |
| Flash Attention | auto, on, off | `on` exige backend suportado |
| KV cache | f16, q8_0, q4_0 | Quantização reduz memória e pode afetar qualidade; confirme suporte no backend |
| Batch lógico | 0 (nativo), 1–8192 | Aumenta demanda de memória |
| Microbatch | 0 (nativo), 1–4096 | Quando ambos definidos, microbatch não pode exceder batch |
| Especulação | off, ngram | `ngram` habilita `--spec-type ngram-simple` e não é um draft remoto |

Confirme a comparação com `/api/bench/inference` antes/depois, usando
mesmo modelo, contexto, quantidade de tokens e condições de rede. O ganho
deve ser registrado separadamente para **prefill**, **decode**, latência do
primeiro token e carregamento; os benchmarks atuais ainda não cobrem
todos esses valores por nó. Um modelo pode não suportar as opções solicitadas;
nesse caso a inicialização falhará explicitamente.

## Cache nativo de tensores RPC — opt-in

No Linux/macOS/Windows Guest, use:

```sh
nodexlm guest --backend cpu --rpc-cache
```

`--rpc-cache` encaminha `--cache` para o ggml-rpc-server da versão fixada
do llama.cpp. Ele **grava pesos de modelos no disco do Guest** e reduz
retransferência de tensores em recarregamentos; NÃO acelera necessariamente
a geração token a token. Utilize **somente máquinas confiáveis**, com disco
criptografado e controles de acesso ao cache. O padrão permanece desativado.
O local pode ser alterado via `LLAMA_CACHE`, cujo subdiretório `rpc`
é administrado pelo servidor nativo. Revogar um nó não remove automaticamente
o conteúdo armazenado no disco dele. O Android não recebeu essa opção.

## Limites atuais e próximas etapas

- Não há escalonamento dinâmico de especialistas MoE; as opções MoE atuais
  delegam a execução ao llama.cpp.
- Não há *draft model* independente rodando em um Guest para speculative
  decoding. O modo n-gram é local ao runtime principal.
- Não há tensor parallelism adaptativo, nem migração de KV cache entre
  processos, nem balanceamento de requisições entre múltiplos clusters
  distribuídos. Modelos residentes adicionais permanecem locais ao Host.
- Antes de atualizar o llama.cpp fixado, é necessário recompilar os bundles
  de RPC, validar interoperabilidade e executar testes físicos com Linux,
  Windows, macOS e Android (inclusive energia e térmica).
- Mantenha a saída como **experimental**, não prometa melhoria de velocidade
  sem benchmark A/B sobre o mesmo hardware.

## Atualização do motor nativo

A revisão do llama.cpp fica fixada em `internal/llamaruntime/revision.json` e os arquivos oficiais SHA-256 em `internal/llamaruntime/install.go`. Ao mudar de revisão, os pares Host/Guest RPC devem ser recompilados juntos (inclusive o patch Unix) e os novos binários publicados e verificados antes de preencher `published`. Enquanto os novos hashes não forem publicados, `EnsureDistributed` tenta a compilação local da mesma revisão. **Não é permitido reutilizar bundles publicados de revisões antigas**. Android compila sua própria biblioteca C++ na revisão fixada e precisa de testes físicos específicos.
