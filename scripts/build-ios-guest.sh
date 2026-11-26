#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
test "$(uname -s)" = Darwin
command -v xcodegen >/dev/null
sdk=$(xcrun --sdk iphoneos --show-sdk-path)
clang=$(xcrun --sdk iphoneos --find clang)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p ios/native
revision=$(python3 -c 'import json;print(json.load(open("internal/llamaruntime/revision.json"))["commit"])')
git init -q "$work/src"
git -C "$work/src" fetch --depth=1 https://github.com/ggml-org/llama.cpp.git "$revision"
git -C "$work/src" checkout -q FETCH_HEAD
test "$(git -C "$work/src" rev-parse HEAD)" = "$revision"
git -C "$work/src" apply "$root/internal/llamaruntime/patches/unix-rpc.patch"
git -C "$work/src" apply "$root/internal/llamaruntime/patches/arm32-rpc-wire.patch"
git -C "$work/src" apply "$root/android/app/src/main/cpp/android-rpc-shutdown.patch"
# The Android stop patch also contains Linux-only CPU affinity. iOS exposes
# Metal only; exclude that block without changing the reviewed wire protocol.
python3 - "$work/src/ggml/src/ggml-rpc/ggml-rpc.cpp" <<'PY'
import pathlib, sys
p = pathlib.Path(sys.argv[1])
s = p.read_text()
start = s.index("        // Own an explicit GGML thread pool")
end = s.index("\n    }\n\n    std::string host;", start)
s = s[:start] + "#if defined(__linux__)\n" + s[start:end] + "\n#endif" + s[end:]
p.write_text(s)
PY
cmake -S "$work/src" -B "$work/build" -G Xcode \
  -DCMAKE_SYSTEM_NAME=iOS -DCMAKE_OSX_SYSROOT=iphoneos -DCMAKE_OSX_ARCHITECTURES=arm64 \
  -DCMAKE_OSX_DEPLOYMENT_TARGET=17.0 -DCMAKE_XCODE_ATTRIBUTE_CODE_SIGNING_ALLOWED=NO \
  -DBUILD_SHARED_LIBS=OFF -DGGML_NATIVE=OFF -DGGML_OPENMP=OFF \
  -DGGML_METAL=ON -DGGML_METAL_EMBED_LIBRARY=ON -DGGML_RPC=ON -DGGML_RPC_RDMA=OFF \
  -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_EXAMPLES=OFF -DLLAMA_BUILD_SERVER=OFF -DLLAMA_BUILD_TOOLS=OFF
cmake --build "$work/build" --config Release --target ggml --parallel "${NODEXLM_BUILD_JOBS:-2}"
find "$work/build" -name 'libggml*.a' -type f > "$work/libraries"
test -s "$work/libraries"
xcrun libtool -static -o ios/native/libNodexGGML.a -filelist "$work/libraries"
cp "$work/src/ggml/include/"*.h ios/native/
# Explicit iOS target flags keep Go's C bridge and native archives on the same SDK.
cat > "$work/cc" <<EOF
#!/bin/sh
exec "$clang" -isysroot "$sdk" -target arm64-apple-ios17.0 "\$@"
EOF
chmod +x "$work/cc"
CGO_ENABLED=1 GOOS=ios GOARCH=arm64 CC="$work/cc" \
  go build -trimpath -buildmode=c-archive -o ios/native/libNodexGo.a ./cmd/nodexlm-ios-guest
(cd ios && xcodegen generate)
xcodebuild -project ios/NodexGuest.xcodeproj -scheme NodexGuest -sdk iphoneos \
  -configuration Release -derivedDataPath ios/build CODE_SIGNING_ALLOWED=NO build
