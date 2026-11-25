#include "ggml.h"
#include "ggml-cpu.h"
#include <cmath>
#include <cstdio>
#include <cstdlib>
#include <initializer_list>
#if !defined(__aarch64__) || !defined(__linux__)
#error This candidate must be tested on Linux ARM64
#endif

static bool run(int width, int rows, int rhs_width, int threads, int op, bool inplace) {
    ggml_init_params params = { 16 * 1024 * 1024, nullptr, false };
    ggml_context *ctx = ggml_init(params);
    if (!ctx) return false;
    auto *a = ggml_new_tensor_2d(ctx, GGML_TYPE_F32, width, rows);
    auto *b = ggml_new_tensor_2d(ctx, GGML_TYPE_F32, rhs_width, rows);
    for (int i = 0; i < width * rows; ++i) ggml_set_f32_1d(a, i, float(i % 31 - 15));
    for (int i = 0; i < rhs_width * rows; ++i) ggml_set_f32_1d(b, i, float(1 << (i % 4)));
    auto *out = op == 0 ? (inplace ? ggml_add_inplace(ctx, a, b) : ggml_add(ctx, a, b))
              : op == 1 ? ggml_mul(ctx, a, b) : ggml_div(ctx, a, b);
    auto *graph = ggml_new_graph(ctx);
    ggml_build_forward_expand(graph, out);
    bool ok = ggml_graph_compute_with_ctx(ctx, graph, threads) == GGML_STATUS_SUCCESS;
    for (int i = 0; ok && i < width * rows; ++i) {
        const float x = float(i % 31 - 15);
        const float y = float(1 << (((i / width) * rhs_width + i % width % rhs_width) % 4));
        const float expected = op == 0 ? x + y : op == 1 ? x * y : x / y;
        ok = ggml_get_f32_1d(out, i) == expected;
    }
    ggml_free(ctx);
    if (!ok) std::fprintf(stderr, "width=%d rows=%d rhs=%d threads=%d op=%d inplace=%d failed\n",
                         width, rows, rhs_width, threads, op, inplace);
    return ok;
}

int main() {
    ggml_cpu_init();
    for (int threads : {1, 3, 4}) {
        for (int op = 0; op < 3; ++op) {
            if (!run(1027, 1, 1027, threads, op, false) ||
                !run(1023, 1, 1023, threads, op, false) ||
                !run(1027, 1, 1, threads, op, false) ||
                !run(1027, 2, 1027, threads, op, false)) return EXIT_FAILURE;
        }
        if (!run(1027, 1, 1027, threads, 0, true)) return EXIT_FAILURE;
    }
    std::puts("Single-row ARM64 numerical checks passed");
    return EXIT_SUCCESS;
}
