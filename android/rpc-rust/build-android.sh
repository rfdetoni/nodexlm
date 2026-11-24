#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
set -euo pipefail
cd "$(dirname "$0")"
output="${1:?Pass absolute generated JNI libraries directory}"
[[ "$output" == /* ]] || { echo "Output must be absolute" >&2; exit 1; }
sdk="${ANDROID_SDK_ROOT:-${ANDROID_HOME:-}}"
ndk="${NODEXLM_ANDROID_NDK:-$sdk/ndk/27.2.12479018}"
case "$(uname -s)" in
  Linux) host=linux-x86_64 ;;
  Darwin) host=darwin-x86_64 ;;
  *) echo "Use Linux or macOS for this experimental build" >&2; exit 1 ;;
esac
bin="$ndk/toolchains/llvm/prebuilt/$host/bin"
test -x "$bin/aarch64-linux-android29-clang"
test -x "$bin/armv7a-linux-androideabi29-clang"
rustup target add --toolchain 1.94.0 aarch64-linux-android armv7-linux-androideabi
test -s Cargo.lock # Never resolve new dependencies during an APK build.
export CARGO_TARGET_AARCH64_LINUX_ANDROID_LINKER="$bin/aarch64-linux-android29-clang"
export CC_aarch64_linux_android="$bin/aarch64-linux-android29-clang"
export AR_aarch64_linux_android="$bin/llvm-ar"
export CARGO_TARGET_ARMV7_LINUX_ANDROIDEABI_LINKER="$bin/armv7a-linux-androideabi29-clang"
export CC_armv7_linux_androideabi="$bin/armv7a-linux-androideabi29-clang"
export AR_armv7_linux_androideabi="$bin/llvm-ar"
export RUSTFLAGS="-C link-arg=-Wl,-z,max-page-size=16384"
for pair in aarch64-linux-android:arm64-v8a armv7-linux-androideabi:armeabi-v7a; do
  target="${pair%:*}"
  abi="${pair#*:}"
  cargo +1.94.0 build --locked --release --target "$target" --target-dir target
  mkdir -p "$output/$abi"
  cp "target/$target/release/libnodexlm_rpc.so" "$output/$abi/libnodexlm_rpc.so"
done
