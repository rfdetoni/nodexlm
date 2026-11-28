// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
// Compiled only against the separately pinned Trellis candidate.
#include "ggml-backend.h"
#include "ggml-rpc.h"
#include "ggml.h"

#include <array>
#include <cstdint>
#include <cmath>
#include <vector>

int main(int argc, char ** argv) {
    GGML_ASSERT(argc == 2 || argc == 3);
    static_assert(RPC_PROTO_MAJOR_VERSION == 10 && RPC_PROTO_MINOR_VERSION == 0 &&
                  RPC_PROTO_PATCH_VERSION == 3, "unexpected Trellis RPC ABI");
    ggml_backend_load_all();
    constexpr std::array<ggml_type, 6> types = {
        GGML_TYPE_TQ2_T, GGML_TYPE_TQK6, GGML_TYPE_TQK7,
        GGML_TYPE_TQP5, GGML_TYPE_TQP6, GGML_TYPE_TQP7,
    };
    ggml_backend_t cpu = ggml_backend_init_by_type(GGML_BACKEND_DEVICE_TYPE_CPU, nullptr);
    GGML_ASSERT(cpu != nullptr);
    for (int endpoint = 1; endpoint < argc; ++endpoint) {
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
        // Exercise compute, not merely transport. A tiny GEMV for each
        // quantization compares the remote device to the pinned local CPU.
        for (auto type : types) {
            constexpr int64_t cols = 256;
            constexpr int64_t rows = 4;
            auto run = [&](ggml_backend_t device) {
                ggml_init_params graph_params = {};
                graph_params.mem_size = 3 * ggml_tensor_overhead() + ggml_graph_overhead_custom(16, false);
                graph_params.no_alloc = true;
                ggml_context * graph_ctx = ggml_init(graph_params);
                GGML_ASSERT(graph_ctx != nullptr);
                auto * weights = ggml_new_tensor_2d(graph_ctx, type, cols, rows);
                auto * input = ggml_new_tensor_1d(graph_ctx, GGML_TYPE_F32, cols);
                auto * result = ggml_mul_mat(graph_ctx, weights, input);
                auto * graph = ggml_new_graph_custom(graph_ctx, 16, false);
                ggml_build_forward_expand(graph, result);
                auto * buf = ggml_backend_alloc_ctx_tensors(graph_ctx, device);
                GGML_ASSERT(buf != nullptr);

                // Every packed trellis path is valid. Use an exact FP16 1.0
                // scale (0x3c00 little-endian) for every block.
                const size_t block_bytes = ggml_type_size(type);
                std::vector<uint8_t> packed(ggml_nbytes(weights));
                for (size_t k = 0; k < packed.size(); k += block_bytes) {
                    packed[k] = 0;
                    packed[k + 1] = 0x3c;
                    for (size_t j = 2; j < block_bytes; ++j) {
                        packed[k + j] = static_cast<uint8_t>(k + j * 17);
                    }
                }
                std::vector<float> activation(cols);
                for (int64_t i = 0; i < cols; ++i) {
                    activation[i] = static_cast<float>(i % 23 - 11) / 16.0f;
                }
                ggml_backend_tensor_set(weights, packed.data(), 0, packed.size());
                ggml_backend_tensor_set(input, activation.data(), 0, ggml_nbytes(input));
                GGML_ASSERT(ggml_backend_graph_compute(device, graph) == GGML_STATUS_SUCCESS);
                std::array<float, rows> out = {};
                ggml_backend_tensor_get(result, out.data(), 0, ggml_nbytes(result));
                ggml_backend_buffer_free(buf);
                ggml_free(graph_ctx);
                return out;
            };
            const auto expected = run(cpu);
            const auto actual = run(backend);
            for (size_t j = 0; j < actual.size(); ++j) {
                GGML_ASSERT(std::isfinite(expected[j]) && std::isfinite(actual[j]));
                GGML_ASSERT(std::fabs(expected[j] - actual[j]) <= 0.002f * (1.0f + std::fabs(expected[j])));
            }
        }
        ggml_backend_free(backend);
    }
    ggml_backend_free(cpu);
    return 0;
}
