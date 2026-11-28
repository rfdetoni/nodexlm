// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
// Compiled only against the separately pinned Trellis candidate.
#include "ggml-backend.h"
#include "ggml-rpc.h"
#include "ggml.h"

#include <array>
#include <cstdint>
#include <vector>

int main(int argc, char ** argv) {
    GGML_ASSERT(argc == 3);
    static_assert(RPC_PROTO_MAJOR_VERSION == 10 && RPC_PROTO_MINOR_VERSION == 0 &&
                  RPC_PROTO_PATCH_VERSION == 3, "unexpected Trellis RPC ABI");
    ggml_backend_load_all();
    constexpr std::array<ggml_type, 6> types = {
        GGML_TYPE_TQ2_T, GGML_TYPE_TQK6, GGML_TYPE_TQK7,
        GGML_TYPE_TQP5, GGML_TYPE_TQP6, GGML_TYPE_TQP7,
    };
    for (int endpoint = 1; endpoint <= 2; ++endpoint) {
        ggml_backend_t backend = ggml_backend_rpc_init(argv[endpoint], 0);
        GGML_ASSERT(backend != nullptr);
        ggml_init_params params = {};
        params.mem_size = types.size() * ggml_tensor_overhead();
        params.no_alloc = true;
        ggml_context * ctx = ggml_init(params);
        GGML_ASSERT(ctx != nullptr);
        std::array<ggml_tensor *, types.size()> tensors = {};
        for (size_t i = 0; i < types.size(); ++i) {
            const int64_t block = ggml_blck_size(types[i]);
            GGML_ASSERT(block == 128);
            tensors[i] = ggml_new_tensor_1d(ctx, types[i], block * 16);
        }
        ggml_backend_buffer_t buffer = ggml_backend_alloc_ctx_tensors(ctx, backend);
        GGML_ASSERT(buffer != nullptr);
        for (size_t i = 0; i < types.size(); ++i) {
            const size_t n = ggml_nbytes(tensors[i]);
            GGML_ASSERT(n > 0 && n <= 65536);
            std::vector<uint8_t> sent(n), got(n);
            for (size_t j = 0; j < n; ++j) {
                sent[j] = static_cast<uint8_t>(j * 31 + i * 17 + endpoint);
            }
            ggml_backend_tensor_set(tensors[i], sent.data(), 0, n);
            ggml_backend_tensor_get(tensors[i], got.data(), 0, n);
            GGML_ASSERT(sent == got);
        }
        ggml_backend_buffer_free(buffer);
        ggml_free(ctx);
        ggml_backend_free(backend);
    }
    return 0;
}
