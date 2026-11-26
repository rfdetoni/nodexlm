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

## Optional automated Linux systemd cold switch

For two trusted Linux machines using the stock `deploy/nodexlm.service` and `deploy/nodexlm-guest.service`, stage the model in the new Host's /var/lib/nodexlm/models and ensure the nodexlm service account owns the directory. The destination Host unit must be installed, inactive and disabled; the destination Guest and original Host must be active. Check capacity and drain requests first. Run during an exclusive maintenance window from a machine with trusted SSH host keys and noninteractive administrator access to the two systems:

```sh
python3 scripts/handoff-systemd.py --old old-host --new new-host \
  --manifest handoff.json --confirm-cold-switch
```

This independently re-hashes the staged GGUFs, disables/stops the old Host and destination Guest before starting the destination Host, checks the local Web console and preserves the old Host's boot enablement state on the new Host. On failure it attempts to fence the destination before restoring the original services. If fencing cannot be confirmed, it deliberately does not restart the old Host. No forced reset, SSH host-key bypass or credential copying is performed. Do not use with custom service paths or ports without review.

The cold-switch operation does not transfer the model, migrate KV/cache/sessions, fence out-of-band manual starts, re-pair nodes, or verify physical inference. After success verify the model and re-pair Guests including the old Host. Automated rollback tests use mocked SSH/systemd; real two-device validation is still required.

## Re-enroll Guests after confirming the cold switch

Re-enrollment is a separate, explicitly confirmed operation because successful pairing replaces a Guest's local credential. On stock Linux systemd installations, list the former Host and the other Guests that should join the new coordinator. Install the stock Guest unit and create its dedicated service account/home on the former Host first. SSH must have independently verified host keys and noninteractive access to the required service operations.

```sh
python3 scripts/handoff-systemd.py --old old-host --new new-host \
  --rejoin-only --confirm-rejoin --endpoint https://192.168.1.10:8443 \
  --guest old-host --guest linux-guest
```

Use the actual new Host private LAN IP, not a DNS hostname. The script verifies the old Host is stopped/disabled and the new Host active, rejects duplicate machine identities, reads the certificate SHA-256 over the trusted SSH channel, obtains a fresh pairing code per Guest, and supplies it through stdin only. It starts each Guest after authenticated enrollment and enables it at boot only after the new Host reports that identity online and RPC-ready. On failure it stops/disables the affected Guest where possible; already migrated Guests remain on the new Host. Retry only the remaining Guests after resolving the problem. Do not automatically restart the old Host after credentials have been changed. Android and non-systemd nodes still require their own enrollment procedure; no physical inference verification or live KV transfer is implied.
