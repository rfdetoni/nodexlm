#!/usr/bin/env bash
# Verify that uncompressed APK libraries are 16 KiB ZIP aligned and
# ARM64 ELF load segments are compatible with 16 KiB Android page sizes.
set -euo pipefail

if [[ $# -ne 1 || ! -f "$1" ]]; then
  echo "Usage: $0 path/to/app.apk" >&2
  exit 2
fi
apk="$1"
sdk_tools="${ANDROID_HOME:?}/build-tools/${ANDROID_BUILD_TOOLS:-35.0.0}"
ndk_tools="$ANDROID_HOME/ndk/${NDK_VERSION:-27.2.12479018}/toolchains/llvm/prebuilt/linux-x86_64/bin"
"$sdk_tools/zipalign" -c -P 16 -v 4 "$apk"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
unzip -q "$apk" 'lib/arm64-v8a/*.so' -d "$tmp"
shopt -s nullglob
libs=("$tmp"/lib/arm64-v8a/*.so)
if [[ ${#libs[@]} -eq 0 ]]; then
  echo "Missing arm64 native libraries" >&2
  exit 1
fi
for lib in "${libs[@]}"; do
  alignments="$("$ndk_tools/llvm-readelf" -lW "$lib" | awk '$1 == "LOAD" {print $NF}')"
  if [[ -z "$alignments" ]]; then
    echo "Missing ELF LOAD segments: $lib" >&2
    exit 1
  fi
  for align in $alignments; do
    if (( align < 0x4000 )); then
      echo "ELF segment misaligned for 16 KiB pages: $lib (alignment $align)" >&2
      exit 1
    fi
  done
done
echo "APK alignment and ARM64 ELF LOAD segments verified for 16 KiB pages"
