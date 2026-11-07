# Pairing and trust boundary

The temporary code authenticates an offer bound to the Host certificate. The client verifies that proof before sending enrollment data. The Host issues a separate per-node token and retains its hash. Node credentials do not grant administration. Revoke a node in the Host console to invalidate its REST and tunnel access.

The protocol is intended for a trusted LAN/VPN. Its custom code/proof scheme is **not an externally audited PAKE**. Do not describe the code review, TLS, PBKDF2 or artifact attestations as an independent cryptographic audit. Deployment beyond that trust model requires an independent assessment or a compatible migration to an audited PAKE implementation.

A code is temporary and rate-limited. A pinned Host certificate should be backed up with its private key; replacing it changes the enrolled identity. Protect the configuration directory and Android app storage. Android uses Keystore-backed node-secret protection. Never paste the Host admin token into a Guest.

Private Unix RPC sockets remove local TCP access on patched POSIX runtimes. They still trust processes under the same account. Windows/legacy loopback RPC needs a dedicated machine/account. None of these measures authenticates a malicious process that already owns the Host account.
