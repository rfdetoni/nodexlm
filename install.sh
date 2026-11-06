#!/bin/sh
# NodexLM user-space installer for Linux and macOS.
# Usage: curl -fsSL https://raw.githubusercontent.com/rfdetoni/nodexlm/main/install.sh | sh
set -eu

REPO='https://github.com/rfdetoni/nodexlm'
CHANNEL=${NODEXLM_CHANNEL:-edge}
INSTALL_DIR=${NODEXLM_INSTALL_DIR:-"${HOME:-}/.local/bin"}

fail() { printf 'NodexLM: %s\n' "$*" >&2; exit 1; }
info() { printf 'NodexLM: %s\n' "$*" >&2; }

[ -n "${HOME:-}" ] || fail 'HOME is not set'
case "$CHANNEL" in
  edge) RELEASE_URL="$REPO/releases/download/nodexlm-edge" ;;
  stable) RELEASE_URL="$REPO/releases/latest/download" ;;
  v[0-9]*)
    case "$CHANNEL" in *[!a-zA-Z0-9._+-]*) fail 'invalid version tag' ;; esac
    RELEASE_URL="$REPO/releases/download/$CHANNEL" ;;
  *) fail 'NODEXLM_CHANNEL must be edge, stable, or a vX.Y.Z tag' ;;
esac

command -v curl >/dev/null 2>&1 || fail 'curl is required'
command -v tar >/dev/null 2>&1 || fail 'tar is required'
command -v mktemp >/dev/null 2>&1 || fail 'mktemp is required'

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail 'unsupported operating system; use install.ps1 for Windows' ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  armv7*|armv6*)
    [ "$os" = linux ] || fail 'unsupported macOS CPU'
    arch=armv7 ;;
  *) fail "unsupported CPU architecture: $(uname -m)" ;;
esac

archive="nodexlm_${os}_${arch}.tar.gz"
mkdir -p "$INSTALL_DIR" || fail "cannot create $INSTALL_DIR"
# Keep all files on the destination filesystem for an atomic final rename.
tmp_dir=$(mktemp -d "$INSTALL_DIR/.nodexlm-install.XXXXXXXX") || fail 'cannot create temporary directory'
cleanup() { rm -rf -- "$tmp_dir"; }
trap cleanup EXIT HUP INT TERM

info "Downloading $archive ($CHANNEL)"
curl --fail --location --silent --show-error --retry 3 --connect-timeout 15 \
  "$RELEASE_URL/SHA256SUMS.txt" -o "$tmp_dir/SHA256SUMS.txt" || fail 'could not download checksums'
curl --fail --location --silent --show-error --retry 3 --connect-timeout 15 \
  "$RELEASE_URL/$archive" -o "$tmp_dir/$archive" || fail 'could not download executable'

# Parse only an exact, anchored checksum record for the selected archive.
expected=$(awk -v name="$archive" '
  $2 == name || $2 == "*" name {
    if ($1 ~ /^[0-9a-fA-F]+$/ && length($1) == 64) { print tolower($1); exit }
  }
' "$tmp_dir/SHA256SUMS.txt")
[ -n "$expected" ] || fail "checksum missing for $archive"

if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp_dir/$archive" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$tmp_dir/$archive" | awk '{print $1}')
elif command -v openssl >/dev/null 2>&1; then
  actual=$(openssl dgst -sha256 "$tmp_dir/$archive" | awk '{print $NF}')
else
  fail 'sha256sum, shasum or openssl is required for checksum verification'
fi
[ "$expected" = "$actual" ] || fail 'SHA-256 mismatch: download rejected'

# Release packages always contain a top-level nodexlm executable.
# Inspect paths to avoid extracting unexpected files or traversal entries.
entries=$(tar -tzf "$tmp_dir/$archive") || fail 'invalid archive'
for entry in $entries; do
  case "$entry" in nodexlm|LICENSE|./nodexlm|./LICENSE) : ;; *) fail "unexpected archive entry: $entry" ;; esac
done
[ -n "$entries" ] || fail 'empty archive'
tar -xzf "$tmp_dir/$archive" -C "$tmp_dir" -- nodexlm || fail 'could not extract NodexLM'
[ -f "$tmp_dir/nodexlm" ] && [ ! -L "$tmp_dir/nodexlm" ] || fail 'archive does not contain a regular executable'
chmod 755 "$tmp_dir/nodexlm" || fail 'cannot set permissions'
"$tmp_dir/nodexlm" version >/dev/null 2>&1 || fail 'binary failed startup/version check'
mv -f "$tmp_dir/nodexlm" "$INSTALL_DIR/nodexlm" || fail 'cannot install binary'
info "Installed: $INSTALL_DIR/nodexlm"
"$INSTALL_DIR/nodexlm" version

# Persist the PATH for shells started after installation; never require sudo.
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    case "$INSTALL_DIR" in
      "$HOME"/.local/bin)
        if [ "$os" = darwin ]; then profile="$HOME/.zprofile"; else profile="$HOME/.profile"; fi
        line='export PATH="$HOME/.local/bin:$PATH"'
        if [ ! -f "$profile" ] || ! grep -Fq "$line" "$profile"; then
          { printf '\n# NodexLM installed by install.sh\n%s\n' "$line"; } >> "$profile" || info "Could not update $profile"
        fi
        info "PATH configured in $profile (open a new terminal to use nodexlm)" ;;
      *) info "Add $INSTALL_DIR to PATH to run nodexlm directly" ;;
    esac ;;
esac
info 'Run: nodexlm host  (or nodexlm help)'
