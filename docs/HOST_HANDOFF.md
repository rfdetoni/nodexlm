# Offline Host handoff (laboratory)
 
This is a cold handoff, not live failover. No KV cache, running requests, private keys, API tokens or enrolled Guest credentials are copied. The old Host remains intact. The target must have sufficient RAM/VRAM for its intended placement and an independently installed matching RPC 10 runtime.

1. On the old Host, export the SHA-256 manifest for a complete GGUF set:
   `nodexlm handoff manifest --source /old/models/model.gguf > handoff.json`.
   For split models, point to the first shard. Keep the manifest on a trusted channel.
2. Expose the files to the destination through an authenticated read-only network mount or copy them via an authenticated transport. On the destination, stage the entire set:
   `nodexlm handoff stage --source /mnt/old/model.gguf --manifest handoff.json --target /local/new-host`.
   The target must not exist; its parent must be a trusted existing local directory. All files are streamed, checked against SHA-256 and verified again at destination; failure removes the incomplete fresh target. No source is removed.
3. Stop and unload the old Host and stop the destination's Guest. Start the new coordinator with `nodexlm host --dir /local/new-host`. It creates a new TLS identity and pairing code. Verify inference with the staged model; explicitly re-pair remaining Guests, including the former Host using `nodexlm guest --rejoin`.
4. Rollback: stop the new Host, restart the original Host using its original directory, and re-pair nodes that changed enrollment. Do not run both coordinators as the same logical cluster. The source model and original Host identity were not modified.

The CLI does not automate service shutdown, capacity placement, re-pairing or KV/session migration. No automatic role swap is claimed. A real distributed test on two devices remains required.
