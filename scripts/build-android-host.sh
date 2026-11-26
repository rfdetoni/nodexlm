#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
out=$(mkdir -p "$1/arm64-v8a" && cd "$1/arm64-v8a" && pwd)
ndk=${ANDROID_NDK_HOME:-${ANDROID_NDK_ROOT:-${ANDROID_HOME:-${ANDROID_SDK_ROOT:-}}/ndk/27.2.12479018}}
test -f "$ndk/build/cmake/android.toolchain.cmake"
toolchain=$(find "$ndk/toolchains/llvm/prebuilt" -mindepth 1 -maxdepth 1 -type d | head -1)
test -x "$toolchain/bin/aarch64-linux-android29-clang"
cd "$root"
CGO_ENABLED=1 GOOS=android GOARCH=arm64 CC="$toolchain/bin/aarch64-linux-android29-clang" \
  go build -trimpath -buildmode=pie -ldflags='-s -w -extldflags=-Wl,-z,max-page-size=16384' -o "$out/libnodexlm_host.so" ./cmd/nodexlm
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
revision=$(python3 -c 'import json;print(json.load(open("internal/llamaruntime/revision.json"))["commit"])')
git init -q "$work/src"
git -C "$work/src" fetch --depth=1 https://github.com/ggml-org/llama.cpp.git "$revision"
git -C "$work/src" checkout -q FETCH_HEAD
test "$(git -C "$work/src" rev-parse HEAD)" = "$revision"
for patch in unix-rpc arm32-rpc-wire parallel-rpc-loader rpc-command-batching; do
  git -C "$work/src" apply "$root/internal/llamaruntime/patches/$patch.patch"
done
cmake -S "$work/src" -B "$work/build" -G Ninja \
  -DCMAKE_TOOLCHAIN_FILE="$ndk/build/cmake/android.toolchain.cmake" \
  -DANDROID_ABI=arm64-v8a -DANDROID_PLATFORM=android-29 -DANDROID_STL=c++_static \
  -DANDROID_SUPPORT_FLEXIBLE_PAGE_SIZES=ON -DCMAKE_BUILD_TYPE=Release \
  -DBUILD_SHARED_LIBS=OFF -DGGML_NATIVE=OFF -DGGML_OPENMP=OFF -DGGML_RPC=ON \
  -DGGML_RPC_RDMA=OFF -DGGML_VULKAN=OFF -DGGML_CPU_KLEIDIAI=OFF \
  -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_EXAMPLES=OFF -DLLAMA_BUILD_TOOLS=ON \
  -DLLAMA_BUILD_SERVER=ON -DLLAMA_CURL=OFF \
  -DLLAMA_BUILD_UI=OFF -DLLAMA_USE_PREBUILT_UI=OFF
cmake --build "$work/build" --target llama-server --parallel "${NODEXLM_BUILD_JOBS:-2}"
install -m 755 "$work/build/bin/llama-server" "$out/libnodexlm_llama_server.so"
"$toolchain/bin/llvm-strip" --strip-unneeded "$out/libnodexlm_llama_server.so"
