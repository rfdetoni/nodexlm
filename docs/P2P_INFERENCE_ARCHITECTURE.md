# P2P-02: Direct RPC Inference Architecture & Upstream Feasibility

## Audited Baseline
- Pinned upstream: `llama.cpp v1.0.0` (`b1b770d90d94cad9e9ea5b993bdbc383a522fb28`)
- RPC wire protocol: `RPC_PROTO_MAJOR_VERSION = 10`, `RPC_PROTO_MINOR_VERSION = 0`, `RPC_PROTO_PATCH_VERSION = 0` (`ggml/include/ggml-rpc.h:9-11`)
- Active NodexLM runtime: `nodexlm-rpc-v1.0.0-r6` (`internal/llamaruntime/revision.json`)

## Observed Runtime Topology & Scheduler Flow
1. **Scheduler resides exclusively on Host**: `ggml_backend_sched` executes inside the Host `llama-server` process. Remote Guests run `ggml-rpc-server`, which accepts one client connection per `rpc_serve_client` session (`ggml/src/ggml-rpc/ggml-rpc.cpp:3043-3052`).
2. **Buffer handles are raw server pointers**: `ggml_backend_rpc_buffer_type_alloc_buffer` (`ggml-rpc.cpp:1004-1030`) returns `remote_ptr` (raw `uint64_t` address in the Guest process). `serialize_tensor` (`ggml-rpc.cpp:769-814`) embeds `remote_ptr` and raw `tensor->data` (`uint64_t`).
3. **Cross-Guest copies bounce through Host**: In `ggml_backend_rpc_buffer_cpy_tensor` (`ggml-rpc.cpp:950-971`):
   - If `src_ctx->dispatcher != dst_ctx->dispatcher`, it logs *"src and dst are on different servers, falling back to a host copy"* and returns `false`.
   - `ggml_backend_tensor_copy` (`ggml/src/ggml-backend.cpp:843-852`) then allocates a temporary Host buffer, calls `ggml_backend_tensor_get` (`RPC_CMD_GET_TENSOR` from Guest A -> Host), and calls `ggml_backend_tensor_set` (`RPC_CMD_SET_TENSOR` from Host -> Guest B).
4. **Why `RPC_CMD_COMM_INIT` is not usable for safe LAN P2P**:
   - `rpc_server::comm_init` (`ggml-rpc.cpp:2335-2365`) only implements collective `allreduce` (`RPC_CMD_COMM_ALLREDUCE`), not pipeline/layer activation copies (`cpy_tensor`).
   - Rank 0 binds `socket_t::create_server("0.0.0.0", request.port)` with unauthenticated raw TCP — violating NodexLM's security invariant against exposing raw unauthenticated RPC ports on LAN.

## Decision & Implemented Stage
- **Do not simulate P2P inference** by forwarding sockets or falsifying metrics.
- **Explicit capability reporting**: `RuntimeCapability.RPCDirectTensorCopy` (`rpc_direct_tensor_copy`) remains `false` (`unavailable` in `ggml-rpc 10.0`), and `internal/rpcmesh/peer_rpc.go` (`CheckWireCompatibility`, `ValidatePeerRPCSession`) fails closed (`ErrDirectTensorCopyUnsupported` / `ErrRPCWireIncompatible`) when a peer or caller requests `direct-tensor-copy-v1` or mismatches major wire `10`.
- **What IS direct Guest <-> Guest today (`P2P-01`)**: Authenticated GGUF weight distribution (`PeerCacheServer` / `FetchFromPeer` / Android `PeerModelCache`), verified by SHA-256 + Ed25519 grants + ECDSA P-256 PoP, with Host receiving zero payload bytes.
- **Path to `10.1` (`r7` candidate)**: Replace raw `remote_ptr`/`tensor->data` in cross-peer copy requests with opaque per-session buffer IDs, add `RPC_CMD_PEER_COPY_TENSOR` bound to a loopback proxy backed by Coordinator-signed `"rpc"` `PeerGrant` + TLS 1.3 pinned tunnels, and rebuild/verify all 6 desktop RPC bundles + Android + iOS together.
