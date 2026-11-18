# Modelos de decisão ONNX integrados no Android

O aplicativo NodexLM Android agora contém o **ONNX Runtime Android** e o
tokenizador Hugging Face usados para inferência local de modelos de decisão
**Laya**. O telefone pode executar uma decisão completa por job via
`decision-worker`, sem outro aplicativo ou serviço `ollaya` em execução.

## Modelos suportados

O primeiro contrato nativo é `onnx/laya-markers-v1`, descrito por quatro
arquivos no mesmo bundle:

- `model.onnx` (+ `model.onnx.data` se o modelo usa pesos externos),
  `tokenizer.json`, `decision.json` e `calibration.json`.
- A conversão **Laya Mobile Q8** também pode ser importada com três arquivos:
  `laya-q8.onnx`, `tokenizer.json` e `rl_agent_config.json`.
  O aplicativo converte a configuração Laya Mobile para o layout System One
  local com tokens especiais e temperaturas explícitas.

Fonte de exemplo: [charioteer/laya-mobile](https://huggingface.co/charioteer/laya-mobile/tree/main)
(licença Apache-2.0). O ONNX Q8 ocupa cerca de **654 MB**, mais aproximadamente
3,6 MB do tokenizador. Baixe os três arquivos dessa fonte antes de importá-los;
eles são **pesos externos ao APK**, porque embarcar centenas de megabytes em
todas as instalações desperdiçaria tráfego e armazenamento.

## Passo a passo

1. Pareie o Android com o Host NodexLM pela aba **Conexão**.
2. Em **Processamento → Worker de decisão**, informe o alias exato de cluster:
   `ollaya/laya:en` (a conversão Laya Mobile é em inglês).
3. Pressione **Importar arquivos do modelo ONNX de decisão**, selecione os
   três arquivos `laya-q8.onnx`, `tokenizer.json` e
   `rl_agent_config.json` de uma única vez. A importação usa pasta temporária
   e só habilita o modelo após a cópia integral e verificação da estrutura.
4. Pressione **Testar runtime de decisão**. A primeira carga precisa de RAM
   disponível e pode demorar; as próximas decisões reutilizam o mesmo modelo.
5. Pressione **Iniciar worker** para anunciar a capacidade ao Host. O
   scheduler selecionará o Android quando esse modelo estiver disponível.

A API no Host permanece `POST /v1/systemone`. O corpo e os resultados
`choice`, `score` e `noul` usam o contrato já existente, sem conversão
para chat nem geração de scores por heurística. O ONNX real é quem gera os
logits; a calibração aplica as temperaturas do bundle.

## Recursos e segurança

- O modelo, o tokenizador e o ONNX Runtime rodam **no processo do próprio
  APK**; não existe porta HTTP local para a inferência embutida.
- A conexão do APK com o Host mantém autenticação por dispositivo e TLS
  com certificado confiável/fixado. Os jobs não indicam caminhos, URLs nem
  modelos arbitrários para executar: o worker aceita apenas o alias instalado.
- Os modelos são copiados para armazenamento privado do aplicativo e
  carregados apenas sob demanda; se não houver RAM suficiente o telefone
  **não anuncia** disponibilidade. A sessão ONNX é fechada quando o worker
  para. No Android, a inferência embedded usa CPU; GPU NNAPI não está
  habilitada sem validação de precisão e estabilidade.
- O modelo não é redistribuído automaticamente entre dispositivos; cada
  worker deve instalar o bundle compatível, mantendo a distribuição de tarefas
  independente da distribuição de camadas do llama.cpp.
- O antigo endpoint Ollaya no loopback continua opcional para outros modelos
  e como compatibilidade, mas não é necessário para Laya ONNX instalado.
- Os executores ONNX disponíveis variam por ABI, página de memória Android e
  operadores do gráfico. Validação CI cobre compilação e contratos puros;
  desempenho, consumo e equivalência numérica com o modelo de referência
  precisam de testes em aparelhos físicos e vetores de ouro.

## Limites conhecidos

Somente bundles Laya com layout `laya-markers-v1` estão habilitados para
inferência ONNX embutida. O APK **não** interpreta automaticamente os layouts
de todas as famílias Ollaya, como GLiClass, NLI, Winnow, JevK5 etc.
Esses modelos continuam usando um backend externo ou o GGUF RPC conforme
compatibilidade. Os metadados de calibração condicionados à entrada e
perguntas `ordinal` não são executados silenciosamente com outro algoritmo:
o runner os rejeita até existir implementação e teste específico.

O modelo Laya Mobile original foi verificado por seus autores em emulador
Android, mas não há neste PR medição em cada dispositivo real ou promessa
de paridade exata em todas as precisões. Para uso sensível, configure políticas
independentes e valide probabilidades com os casos de referência.

Referência de arquitetura: [Ollaya laya-markers-v1](https://github.com/ollaya-dev/ollaya/blob/main/crates/ollaya-decision/src/layout.rs).
