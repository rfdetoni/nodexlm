#!/usr/bin/env bash
# Build a separate, pinned experimental runtime. No stock runtime is replaced.
set -euo pipefail
revision=dc5521842f7991ceae73139f9d242d8f877e12d4
backend=${1:-cpu}
case "$backend" in
  cpu) flags=(-DGGML_VULKAN=OFF -DGGML_CUDA=OFF -DGGML_METAL=OFF) ;;
  vulkan) flags=(-DGGML_VULKAN=ON -DGGML_CUDA=OFF -DGGML_METAL=OFF) ;;
  cuda) flags=(-DGGML_VULKAN=OFF -DGGML_CUDA=ON -DGGML_METAL=OFF) ;;
  metal) flags=(-DGGML_VULKAN=OFF -DGGML_CUDA=OFF -DGGML_METAL=ON) ;;
  *) echo "usage: $0 [cpu|vulkan|cuda|metal]" >&2; exit 2 ;;
esac
root="${NODEXLM_HOME:-$HOME/.nodexlm}/runtimes/trellis/$revision"
dest="$root/$backend"
mkdir -p "$root"
work=$(mktemp -d "$root/.build.XXXXXXXX")
trap 'rm -rf -- "$work"' EXIT
git init -q "$work/src"
git -C "$work/src" remote add origin https://github.com/halo-box/strix-llama.cpp.git
git -C "$work/src" fetch --depth=1 origin "$revision"
git -C "$work/src" checkout -q --detach FETCH_HEAD
test "$(git -C "$work/src" rev-parse HEAD)" = "$revision"
cmake -S "$work/src" -B "$work/build" -DCMAKE_BUILD_TYPE=Release \
  -DGGML_RPC=OFF -DGGML_NATIVE="${NODEXLM_TRELLIS_NATIVE:-ON}" -DGGML_OPENMP=OFF \
  -DBUILD_SHARED_LIBS=OFF -DLLAMA_CURL=OFF -DLLAMA_BUILD_TESTS=OFF \
  -DLLAMA_BUILD_EXAMPLES=OFF -DLLAMA_BUILD_TOOLS=ON -DLLAMA_BUILD_SERVER=ON \
  -DLLAMA_BUILD_UI=OFF -DLLAMA_USE_PREBUILT_UI=OFF "${flags[@]}"
cmake --build "$work/build" --config Release --parallel "${NODEXLM_BUILD_JOBS:-2}" --target llama-server
mkdir -p "$work/install/bin"
binary="$work/build/bin/llama-server"
if [[ ! -f "$binary" ]]; then binary="$work/build/bin/Release/llama-server.exe"; fi
if [[ ! -f "$binary" ]]; then binary="$work/build/bin/llama-server.exe"; fi
test -f "$binary"
"$binary" --help > "$work/help.txt" 2>&1
grep -F -- '--ple' "$work/help.txt" >/dev/null
cp "$binary" "$work/install/bin/"
cp "$work/src/LICENSE" "$work/install/"
printf '%s\n' "$revision" > "$work/install/source-commit.txt"
if [[ -e "$dest" ]]; then
  echo "Runtime already exists at $dest; remove it explicitly before rebuilding." >&2
  exit 1
fi
mv "$work/install" "$dest"
echo "Installed separate Trellis candidate: $dest/bin"
