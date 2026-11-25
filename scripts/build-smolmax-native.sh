#!/usr/bin/env bash
# Copyright (C) 2026 Rodrigo Felipe de Toni Grave
# SPDX-License-Identifier: AGPL-3.0-only
# Build a SEPARATE opt-in llama-server from the smolmaxxing experimental patch.
# Does not modify the production llama.cpp or any NodexLM RPC artifacts.
set -euo pipefail

DEST="${1:-$PWD/build/smolmax-native}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LLAMA_COMMIT=b1b770d90d94cad9e9ea5b993bdbc383a522fb28
# The full patch is a reviewed port to this exact revision. Never silently rebase.
python3 - "$ROOT/internal/llamaruntime/revision.json" "$LLAMA_COMMIT" <<'PY'
import json, sys
assert json.load(open(sys.argv[1]))["commit"] == sys.argv[2], "Smolmax port must be reviewed for the new llama.cpp revision"
PY

if [[ "$(uname -s)" != "Linux" || "$(uname -m)" != "aarch64" ]]; then
  echo 'Full Smolmax candidate requires Linux ARM64; Android and other architectures are not yet accepted' >&2
  exit 1
fi
for tool in git cmake python3; do
  command -v "$tool" >/dev/null || { echo "missing dependency: $tool" >&2; exit 1; }
done

mkdir -p "$DEST"
DEST="$(cd "$DEST" && pwd)"
if [[ -e "$DEST/llama-server" || -e "$DEST/llama-server.smolmax.json" || -e "$DEST/llama-bench" || -e "$DEST/llama-perplexity" ]]; then
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
PATCH="$ROOT/experiments/smolmax/full-v1.patch"
test -s "$PATCH"
git -C "$TMP/llama" apply --check "$PATCH"
git -C "$TMP/llama" apply "$PATCH"

cmake -S "$TMP/llama" -B "$TMP/build" \
  -DCMAKE_BUILD_TYPE=Release \
  -DGGML_NATIVE=OFF -DGGML_RPC=OFF -DGGML_OPENMP=OFF \
  -DBUILD_SHARED_LIBS=OFF -DLLAMA_CURL=OFF \
  -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_EXAMPLES=OFF -DLLAMA_BUILD_TOOLS=ON \
  -DLLAMA_BUILD_SERVER=ON -DLLAMA_BUILD_UI=OFF -DLLAMA_USE_PREBUILT_UI=OFF
cmake --build "$TMP/build" --parallel "${NODEXLM_BUILD_JOBS:-2}" --target llama-server llama-bench llama-perplexity
test -x "$TMP/build/bin/llama-server"
for binary in llama-server llama-bench llama-perplexity; do
  install -m 0755 "$TMP/build/bin/$binary" "$DEST/$binary"
done
install -m 0644 "$ROOT/experiments/smolmax/LICENSE" "$DEST/LICENSE.smolmax"
install -m 0644 "$TMP/llama/LICENSE" "$DEST/LICENSE.llama.cpp"
python3 "$ROOT/scripts/register-smolmax-runtime.py" \
  --binary "$DEST/llama-server" --features moe_pager weight_streaming

printf '\nBuilt EXPERIMENTAL native server: %s\n' "$DEST/llama-server"
printf 'Test --help flags and model compatibility BEFORE selecting it with --llama-bin.\n'
printf 'Do not expose its port; NodexLM binds the managed server to loopback.\n'
