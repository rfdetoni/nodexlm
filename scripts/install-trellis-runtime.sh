#!/usr/bin/env bash
# Build a separate, pinned experimental runtime. No stock runtime is replaced.
set -euo pipefail
revision=dc5521842f7991ceae73139f9d242d8f877e12d4
backend=${1:-cpu}
mode=${2:-local}
case "$mode" in
  local) rpc=OFF; tests=OFF; suffix="" ;;
  rpc-candidate) rpc=ON; tests=ON; suffix="-rpc-candidate" ;;
  *) echo "usage: $0 [cpu|vulkan|cuda|metal] [local|rpc-candidate]" >&2; exit 2 ;;
esac
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
case "$backend" in
  cpu) flags=(-DGGML_VULKAN=OFF -DGGML_CUDA=OFF -DGGML_METAL=OFF) ;;
  vulkan) flags=(-DGGML_VULKAN=ON -DGGML_CUDA=OFF -DGGML_METAL=OFF) ;;
  cuda) flags=(-DGGML_VULKAN=OFF -DGGML_CUDA=ON -DGGML_METAL=OFF) ;;
  metal) flags=(-DGGML_VULKAN=OFF -DGGML_CUDA=OFF -DGGML_METAL=ON) ;;
  *) echo "usage: $0 [cpu|vulkan|cuda|metal]" >&2; exit 2 ;;
esac
root="${NODEXLM_HOME:-$HOME/.nodexlm}/runtimes/trellis/$revision"
dest="$root/$backend$suffix"
mkdir -p "$root"
work=$(mktemp -d "$root/.build.XXXXXXXX")
trap 'rm -rf -- "$work"' EXIT
git init -q "$work/src"
git -C "$work/src" remote add origin https://github.com/halo-box/strix-llama.cpp.git
git -C "$work/src" fetch --depth=1 origin "$revision"
git -C "$work/src" checkout -q --detach FETCH_HEAD
test "$(git -C "$work/src" rev-parse HEAD)" = "$revision"
if [[ "$mode" == rpc-candidate ]]; then
  cp "$script_dir/../internal/llamaruntime/testdata/trellis-rpc-tensors.cpp" "$work/src/tools/rpc/nodexlm-trellis-rpc-tensors.cpp"
  cat >> "$work/src/tools/rpc/CMakeLists.txt" <<'CMAKE'

add_executable(nodexlm-trellis-rpc-tensors nodexlm-trellis-rpc-tensors.cpp)
target_link_libraries(nodexlm-trellis-rpc-tensors PRIVATE ggml ggml-rpc)
target_compile_features(nodexlm-trellis-rpc-tensors PRIVATE cxx_std_17)
add_test(NAME test-rpc-trellis-tensors
    COMMAND bash ${PROJECT_SOURCE_DIR}/tests/test-rpc-multi-server.sh
        $<TARGET_FILE:ggml-rpc-server> $<TARGET_FILE:nodexlm-trellis-rpc-tensors>)
CMAKE
fi
cmake -S "$work/src" -B "$work/build" -DCMAKE_BUILD_TYPE=Release \
  -DGGML_RPC="$rpc" -DGGML_NATIVE="${NODEXLM_TRELLIS_NATIVE:-ON}" -DGGML_OPENMP=OFF \
  -DGGML_BACKEND_DL=OFF -DBUILD_SHARED_LIBS=OFF -DLLAMA_CURL=OFF -DLLAMA_BUILD_TESTS="$tests" \
  -DLLAMA_BUILD_EXAMPLES=OFF -DLLAMA_BUILD_TOOLS=ON -DLLAMA_BUILD_SERVER=ON \
  -DLLAMA_BUILD_UI=OFF -DLLAMA_USE_PREBUILT_UI=OFF "${flags[@]}"
targets=(llama-server)
if [[ "$mode" == rpc-candidate ]]; then
  targets+=(ggml-rpc-server test-rpc-multi-server test-rpc-unreachable nodexlm-trellis-rpc-tensors)
fi
cmake --build "$work/build" --config Release --parallel "${NODEXLM_BUILD_JOBS:-2}" --target "${targets[@]}"
if [[ "$mode" == rpc-candidate ]]; then
  ctest --test-dir "$work/build" -C Release --output-on-failure --no-tests=error \
    -R '^test-rpc-(multi-server|unreachable|trellis-tensors)$'
fi
mkdir -p "$work/install/bin"
binary="$work/build/bin/llama-server"
if [[ ! -f "$binary" ]]; then binary="$work/build/bin/Release/llama-server.exe"; fi
if [[ ! -f "$binary" ]]; then binary="$work/build/bin/llama-server.exe"; fi
test -f "$binary"
"$binary" --help > "$work/help.txt" 2>&1
grep -F -- '--ple' "$work/help.txt" >/dev/null
cp "$binary" "$work/install/bin/"
if [[ "$mode" == rpc-candidate ]]; then
  worker="$work/build/bin/ggml-rpc-server"
  if [[ ! -f "$worker" ]]; then worker="$work/build/bin/Release/ggml-rpc-server.exe"; fi
  if [[ ! -f "$worker" ]]; then worker="$work/build/bin/ggml-rpc-server.exe"; fi
  test -f "$worker"
  "$worker" --help > "$work/rpc-help.txt" 2>&1
  cp "$worker" "$work/install/bin/"
  selftest="$work/build/bin/nodexlm-trellis-rpc-tensors"
  if [[ ! -f "$selftest" ]]; then selftest="$work/build/bin/Release/nodexlm-trellis-rpc-tensors.exe"; fi
  if [[ ! -f "$selftest" ]]; then selftest="$work/build/bin/nodexlm-trellis-rpc-tensors.exe"; fi
  test -f "$selftest"
  cp "$selftest" "$work/install/bin/"
fi
cp "$work/src/LICENSE" "$work/install/"
printf '%s\n' "$revision" > "$work/install/source-commit.txt"
(
  cd "$work/install"
  if command -v sha256sum >/dev/null; then
    sha256sum source-commit.txt LICENSE bin/* > runtime-integrity.sha256
  else
    shasum -a 256 source-commit.txt LICENSE bin/* > runtime-integrity.sha256
  fi
)
if [[ -e "$dest" ]]; then
  echo "Runtime already exists at $dest; remove it explicitly before rebuilding." >&2
  exit 1
fi
mv "$work/install" "$dest"
echo "Installed separate Trellis candidate: $dest/bin"
