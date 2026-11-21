#!/usr/bin/env bash
# Copyright (C) 2026 Rodrigo Felipe de Toni Grave
# SPDX-License-Identifier: AGPL-3.0-only
# Build a SEPARATE opt-in llama-server from the smolmaxxing experimental patch.
# Does not modify the production llama.cpp or any NodexLM RPC artifacts.
set -euo pipefail

LLAMA_COMMIT=8642541b255bfb5dc39cd351a178be09013a55d6
SMOLMAX_COMMIT=8d12bb17fd389c25f3c6167a5b73f93034d6324f
DEST="${1:-$PWD/build/smolmax-native}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ "$(uname -s)" != "Linux" ]]; then
  echo 'smolmax native O_DIRECT/ARM pager requires Linux/Android' >&2
  exit 1
fi
for tool in git cmake python3; do
  command -v "$tool" >/dev/null || { echo "missing dependency: $tool" >&2; exit 1; }
done

mkdir -p "$DEST"
DEST="$(cd "$DEST" && pwd)"
if [[ -e "$DEST/llama-server" || -e "$DEST/llama-server.smolmax.json" ]]; then
  echo 'target already contains a runtime or manifest; refusing overwrite' >&2
  exit 1
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
fetch_pinned() {
  local repository="$1" commit="$2" dest="$3"
  git init -q "$dest"
  git -C "$dest" remote add origin "$repository"
  git -C "$dest" fetch -q --depth=1 origin "$commit"
  git -C "$dest" checkout -q --detach FETCH_HEAD
  test "$(git -C "$dest" rev-parse HEAD)" = "$commit"
}

fetch_pinned https://github.com/ggml-org/llama.cpp.git "$LLAMA_COMMIT" "$TMP/llama"
fetch_pinned https://github.com/hyperionaidev/smolmaxxing.git "$SMOLMAX_COMMIT" "$TMP/smolmax"
PATCH="$TMP/smolmax/patches/llama.cpp-v0.8.0-tvbox.patch"
test -s "$PATCH"
git -C "$TMP/llama" apply --check "$PATCH"
git -C "$TMP/llama" apply "$PATCH"

cmake -S "$TMP/llama" -B "$TMP/build" \
  -DCMAKE_BUILD_TYPE=Release \
  -DGGML_NATIVE=OFF -DGGML_RPC=OFF -DGGML_OPENMP=OFF \
  -DBUILD_SHARED_LIBS=OFF -DLLAMA_CURL=OFF \
  -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_SERVER=ON
cmake --build "$TMP/build" --parallel "${NODEXLM_BUILD_JOBS:-2}" --target llama-server
test -x "$TMP/build/bin/llama-server"
cp "$TMP/build/bin/llama-server" "$DEST/llama-server"
chmod 0755 "$DEST/llama-server"
python3 "$ROOT/scripts/register-smolmax-runtime.py" \
  --binary "$DEST/llama-server" --features moe_pager weight_streaming

printf '\nBuilt EXPERIMENTAL native server: %s\n' "$DEST/llama-server"
printf 'Test --help flags and model compatibility BEFORE selecting it with --llama-bin.\n'
printf 'Do not expose its port; NodexLM binds the managed server to loopback.\n'
