# NodexLM: pairing devices on a local network

## Simple onboarding

**Host (Linux/Windows/macOS, or another computer with the Go CLI):**

```sh
nodexlm host
```

The host creates its own TLS identity and private admin/join credentials, opens TCP 8443 (HTTPS), starts a small UDP discovery listener on port 46465, and prints an **8-character** code such as `K7M2X8Q9` (example only). No token or certificate needs to be copied to guests.

**Guest (Linux/Windows/macOS):**

```sh
nodexlm join
# Enter host pairing code: [type the 8 characters]
```

or `nodexlm join --code K7M2X8Q9` (this form may leave the temporary code in shell history and the process list). For desktop UI, use **Cluster > Join a cluster**. The desktop package must include the `nodexlm` CLI next to the application or in `PATH`.

**Guest (Android):** Open NodexLM, enter the 8-character code, tap **Join local cluster securely**, then import a GGUF file and start the foreground worker. The phone may keep computing with the screen off, subject to Android and vendor power-management restrictions. A system notification permits stopping the worker.

After joining, a machine has its own node ID and a randomly generated long-lived credential stored in local private configuration. The host pairing code is not used for normal requests.

## Starting inference on a joined desktop worker

```sh
nodexlm node --model model.gguf --model-path ./models/model.gguf
```

`agent.json` stores the registered endpoint, per-device bearer credential and a SHA-256 pin for the coordinator's TLS certificate. The worker reuses the pin on subsequent HTTPS calls without needing to import a CA certificate.

## If broadcast discovery is blocked

A manually entered HTTPS URL is supported; the guest still checks that the TLS certificate matches the temporary code before sending credentials:

```sh
nodexlm join --server https://192.168.1.10:8443
# Enter host pairing code:
```

On Android, fill **Coordinator HTTPS URL** before tapping **Join local cluster securely**. Peers must share a locally attached IPv4 subnet; Wi-Fi client isolation or firewall rules can prevent discovery. Do not bridge pairing to public Internet or proxy it through a reverse proxy.

Network ports on host:

- `8443/tcp` — encrypted coordinator communication
- `46465/udp` — LAN-only discovery

## Adding more nodes or renewing the code

One code is valid for **10 minutes**, with up to 16 successful joins, 5 incorrect attempts per source IP, and 40 failures per code window. The host can start a fresh window whenever required:

```sh
nodexlm pairing
```

The host desktop has **Generate host code**. Rotation invalidates the previous code. Existing paired machines keep their own credentials and are unaffected.

To revoke an individual machine:

```sh
nodexlm nodes
nodexlm revoke --id NODE_ID
```

After revocation, that node's credential cannot lease jobs or send results.

## Security design and limitations

1. The coordinator creates a short-lived random 8-character code from a 32-symbol alphabet (40 bits of entropy). `I`, `O`, `0` and `1` are excluded to minimize transcription errors.
2. The host advertises *only* a nonce, salt and PBKDF2-HMAC-SHA256 (200,000 iterations) commitment of the code bound to the exact TLS certificate. The code never appears in a LAN advertisement.
3. Before sending the code, the guest checks that the coordinator's TLS certificate matches the commitment recomputed with the typed code. The guest pins that certificate for subsequent HTTPS enrollment and execution.
4. The host requires the guest's temporary code during enrollment, verifies it with constant-time comparison, rejects off-subnet enrollment, rate-limits attempts, and issues a separate random credential for the guest.
5. The coordinator persists its own long-lived secrets in a private `0600` file; the guest stores its bearer token and certificate pin in private app configuration. Android's app-private storage is used and Android backups are disabled.

**Important:** This is a custom code-to-TLS-binding bootstrap, **not a standardized, audited PAKE such as SPAKE2+ or OPAQUE**. The public proof technically allows an offline dictionary attack; a 40-bit random code plus the slow KDF, short expiry and rate limiting mitigates risk but does not provide PAKE's security guarantees. Do not use it for public-network onboarding or high-assurance production deployments before security review or replacement with a vetted PAKE implementation. Transport after successful binding uses standard TLS with pinned certificates. Client-side interface tests and real Android hardware tests remain necessary.

Additional limits: only direct IPv4 LAN subnet matching and UDP broadcast discovery are implemented; IPv6, mDNS, relay mode, and remote WAN pairing are not implemented. A changed coordinator TLS certificate requires re-pairing. A device may be registered without any model installed, but it will not execute inference until a worker and an available model are configured.
