# NodexLM Security Audit Preparation & Threat Model (`SEC-01`)

> **Audit Status:** External independent cryptographic audit is **PENDING** (`BLOQUEADO / EXTERNO`). Internal tests, fuzzing, and CI security scanners do **not** substitute for an independent specialist audit.

## 1. System Trust Boundaries & Principals
1. **Coordinator / Host (`cmd/nodexlm`)**: Loopback-only administrative dashboard & OpenAI control plane (`Dashboard.Handler()`, `:8787`), separate from LAN Guest/pairing HTTPS listener (`Server.LANHandler()`, `:8443`).
2. **Enrolled Guests (Desktop Go / Android Kotlin+Rust / iOS Metal)**: Hold per-node bearer credential (`X-Nodex-Node` + `Authorization: Bearer`) and pin the Host TLS certificate SHA-256 (`PinnedHostTls`, `UsePinnedCertificate`).
3. **Direct P2P Cache Peers (`P2P-01`)**: Guests serving verified SHA-256 GGUF files directly over TLS 1.3 with per-node self-signed ECDSA P-256 TLS leaf certificates and Coordinator-attested `PeerGrant` + per-request ECDSA P-256 proof-of-possession.
4. **OpenAI API Consumers**: Restricted `NODEXLM_API_KEY` permitted ONLY on `/v1/chat/completions`, `/v1/responses`, `/v1/models`, `/v1/images/generations`. Denied (`401/403`) on `/api/*`, `/v1/nodes/*`, `/v1/pairing/*`, `/v1/bench/*`, `/v1/handoff/*`.
5. **Untrusted LAN Attacker / Revoked Peer / Compromised CDN**: Cannot access unauthenticated raw `ggml-rpc-server` (loopback-only), cannot forge Coordinator Ed25519 grants/transitions, cannot perform SSRF (`ValidatePrivatePeerAddress` rejects link-local/metadata/hostnames/public IPs), and cannot bypass SHA-256 + GGUF validation.

## 2. Cryptographic Primitives Inventory
| Flow | Primitives | Standard Implementation | Location |
|---|---|---|---|
| Pairing Commitment | PBKDF2-HMAC-SHA256 (200,000 rounds, 16-byte random salt) + HMAC-SHA256 over `"nodexlm-pairing-v1\0" \|\| cert_sha256 \|\| nonce` | Go `crypto/pbkdf2`, `crypto/hmac`, `crypto/sha256`; Android `PBKDF2WithHmacSHA256` | `internal/pairing/pairing.go`, `PairingClient.kt` |
| Transport Security | TLS 1.3 with exact leaf certificate SHA-256 pinning (no CA/hostname bypass on LAN; platform trust store for public Hugging Face) | Go `crypto/tls`, Android `TrustManager` + `HostnameVerifier`, Rust `rustls` + `ring` | `internal/netclient/client.go`, `PinnedHostTls.kt`, `android/rpc-rust/src/lib.rs` |
| Cluster Authority & Grants | Ed25519 signature over canonical JSON (`PeerGrant`, `EpochTransition`) | Go `crypto/ed25519` | `internal/protocol/peer_grant.go`, `internal/coordinator/authority.go`, `handoff.go` |
| Peer Proof-of-Possession | ECDSA P-256 (`SHA256withECDSA`, ASN.1 DER) over `method\npath\nts_ms\nreq_nonce\ngrant_nonce` + bounded replay ring | Go `crypto/ecdsa`, Android `KeyPairGenerator("EC")`/`secp256r1` | `internal/protocol/peer_grant.go`, `PeerModelCache.kt` |
| Content Integrity | Full-file SHA-256 + `"GGUF"` magic check before atomic publish (`0600`) | Go `crypto/sha256`, Rust `ring::digest::SHA256`, Java `MessageDigest` | `internal/safedownload/`, `android/rpc-rust/src/download.rs` |

## 3. Protocol Flows & Security Invariants
- **Pairing (`internal/pairing/pairing.go`)**: 8-character code (`32^8 = 2^40` bits entropy, uniform bitmask, max 10–30m TTL, max 5 failed attempts per IP, max 40 total failures). Code is never used as a bearer token; enrollment issues an independent 256-bit random per-node token stored hashed (SHA-256) on Host.
- **P2P-01 Grants**: Coordinator signs `{version:1, cluster_id, epoch, issuer:"coordinator", source_node_id, target_peer_id, target_pub_key, scope:"cache", sha256, size, issued_at, expires_at<=5m, nonce}`. Source peer checks Ed25519 signature, cluster ID, epoch floor, self node ID, non-revocation of both nodes, AND verifies downloader's `X-Nodex-Peer-Proof` ECDSA signature + 30s clock window + non-replayed `req_nonce`.
- **MIG-01 Fencing**: Old Host increments `epoch`, persists `fenced: true` (`0600`), refuses new jobs/leases/tunnels, and signs `EpochTransition` binding `new_epoch > old_epoch` and `new_host_cert_sha256`.

## 4. Known Cryptographic Limits & Questions for External Auditor
1. **Custom Pairing vs Standard PAKE**: Current pairing uses PBKDF2 + HMAC commitment over TLS rather than a standardized balanced/augmented PAKE (e.g., SPAKE2+ or OPAQUE). While rate-limited and TLS-bound, does the 40-bit pairing code + 200k PBKDF2 offline brute-force margin on captured discovery offers require upgrading to SPAKE2+ across Go/Android/iOS?
2. **Clock Skew Bounds**: `PeerGrant`, `VerifyPeerRequest` (±30s), and `EpochTransition` rely on UTC wall clocks with small skew tolerance. Evaluate resilience against skewed embedded/Android RTCs.
3. **Authority Key Rotation**: Currently `cluster.authority.json` stores one Ed25519 private key per coordinator home (`0600`). Recommend whether dual-key overlap rotation should be added before production GA.
4. **Fuzzing & Test Vectors**: Reproducible vectors are verified in `internal/protocol/peer_grant_test.go`, `internal/coordinator/handoff_test.go`, `internal/pairing/pairing_fuzz_test.go`, `internal/local/gguf_fuzz_test.go`, `internal/llamaruntime/archive_fuzz_test.go`.
