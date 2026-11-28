# NodexLM Benchmarks

[Metodologia](METHODOLOGY.md) · [Schema de exportação](result.schema.json)

Use o painel Web > Benchmarks, ou scripts/benchmark-cluster.py para coletar
resultados privados A/B. Exporte JSON público pelo painel e acrescente um
relatório de hardware e condições de teste seguindo a metodologia. Armazene
arquivos revisados em results/. Não há resultados oficiais inventados.

Gere uma página estática sem dependências:

    python3 benchmarks/render.py --output /tmp/nodexlm-benchmarks/index.html

Publique o diretório gerado via GitHub Pages, após habilitar Pages no
repositório. O gerador não envia dados para terceiros.

## CLI e estabilidade

    nodexlm benchmark run --model model.gguf --profile compare --rounds 5 --output private.json
    nodexlm benchmark run --model model.gguf --profile stability --node cluster --minutes 60 --output stability.json
    nodexlm benchmark compare --baseline old.json --current new.json

CLI reports are private (native placement information may identify nodes).
The browser exports a reduced public format. Stability uses one loaded runtime
for the entire timed workload; failures abort rather than invent a success.
Before/after device snapshots are not peak memory readings. Energy is
optional, manually measured across the entire stability run; no energy
sensor is simulated.

Enable SHA-256 in the browser or use --hash-model on the CLI to include
the actual identity of each model shard; hashing is opt-in and streams
the entire file with bounded RAM.

GitHub Pages can deploy the static report using the Benchmarks Pages
workflow once repository Settings → Pages → GitHub Actions is enabled.
No hardware results are uploaded automatically.

### Live progress

The dashboard uses an authenticated NDJSON request stream. Each stage is
reported by the actual benchmark executor, with one-second heartbeats during
long operations. Model loading remains indeterminate; selected Guest RPC
traffic is measured as monotonic relay deltas, not unique model bytes.
Round and sample progress use completed counts. Stability shows elapsed
planned duration, not a prediction of completion. Partial measurements are
not exported as completed reports. Existing JSON endpoints remain supported.

Benchmark loading, hashing and inference have no fixed wall-clock deadline.
Cancel the dashboard request or press Ctrl+C in the CLI to stop it. The
stability duration measures inference after warmup, excluding model loading.
Short RPC handshake, connection and health-probe timeouts remain in place.

The preplanned RPC loader groups all weight descriptors by the native
endpoint before any transfer starts, rather than discovering workers while
loading each context. CPU/GPU on one Guest share the native dispatcher.
Mapped weights require no staging copy; buffered weights use one 1 MiB
scratch buffer per endpoint. Direct IO and tensor-validation mode continue
to use the native sequential path. A new immutable native bundle is
required to deploy this patch; the old r1 binary is not modified in place.

Latency tuning compares total request time with equal prompt/output counts and fixed native topology. Native scheduler and Flash Attention candidates are explicit experiments; native output placement reserves all non-block tensor bytes on the final device. RPC traffic windows split at the first observed SSE event (not exact native phase boundaries). The cost-based speed planner uses compatible benchmarks and RTT where available. NODEXLM_RPC_COST_PLACEMENT=0 and NODEXLM_RPC_BATCH_COMMANDS=0 disable the respective optimizations.

Model compatibility: stock GGML TQ2_0 uses type 35; Gyro uses types 144–149. See [Trellis runtime](../docs/TRELLIS.md) for the separate experimental Host-only runtime and external PLE requirements.
