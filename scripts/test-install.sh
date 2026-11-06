#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT HUP INT TERM
mkdir -p "$TMP/release" "$TMP/mock-bin" "$TMP/home"
[ -x "$ROOT/nodexlm" ] || (cd "$ROOT" && go build -o nodexlm ./cmd/nodexlm)
cp "$ROOT/nodexlm" "$TMP/release/nodexlm"
(cd "$TMP/release" && tar -czf nodexlm_linux_amd64.tar.gz nodexlm)
(cd "$TMP/release" && sha256sum nodexlm_linux_amd64.tar.gz > SHA256SUMS.txt)
cat > "$TMP/mock-bin/curl" <<'MOCK'
#!/bin/sh
set -eu
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) out=$2;shift 2;;
    http*|https*) url=$1;shift;;
    *) shift;;
  esac
done
case "$url" in
  */nodexlm_linux_amd64.tar.gz) cp "$MOCK_RELEASE/nodexlm_linux_amd64.tar.gz" "$out";;
  */SHA256SUMS.txt) cp "$MOCK_RELEASE/SHA256SUMS.txt" "$out";;
  *) exit 22;;
esac
MOCK
chmod +x "$TMP/mock-bin/curl"
export MOCK_RELEASE="$TMP/release" HOME="$TMP/home" NODEXLM_INSTALL_DIR="$TMP/home/bin" NODEXLM_CHANNEL=edge PATH="$TMP/mock-bin:$PATH"
sh -n "$ROOT/install.sh"
sh "$ROOT/install.sh" > "$TMP/ok.log" 2>&1
"$TMP/home/bin/nodexlm" version > "$TMP/version.log"
sh "$ROOT/install.sh" >> "$TMP/ok.log" 2>&1
# Corrupt the expected hash and assert fail-closed, without overwriting prior install.
printf '%064d  nodexlm_linux_amd64.tar.gz\n' 0 > "$TMP/release/SHA256SUMS.txt"
if sh "$ROOT/install.sh" > "$TMP/fail.log" 2>&1; then
  echo 'installer accepted a mismatched SHA256' >&2
  exit 1
fi
grep -q 'SHA-256 mismatch' "$TMP/fail.log"
"$TMP/home/bin/nodexlm" version >/dev/null
printf '%s\n' 'POSIX installer smoke test: PASS (download, re-install, checksum rejection)'
