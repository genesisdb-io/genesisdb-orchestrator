#!/usr/bin/env bash
#
# GenesisDB Orchestrator installer.
#
# Usage:
#   curl -fsSL https://orchestrator.genesisdb.io/install.sh | bash
#   curl -fsSL https://orchestrator.genesisdb.io/install.sh | bash -s -- v0.0.2 ~/bin
#
# Positional arguments:
#   $1  Release tag, for example v0.0.2. Defaults to latest.
#   $2  Install directory. Defaults to the first writable directory in
#       /usr/local/bin, $HOME/.local/bin, and $HOME/bin.
#
# Environment overrides:
#   GENESISDB_VERSION  Same as $1.
#   GENESISDB_PREFIX   Same as $2.
#   GITHUB_TOKEN       Token used for private GitHub release downloads.

set -euo pipefail

OWNER="genesisdb-io"
REPO="genesisdb-orchestrator"
BINARY="genesisdb"
VERSION="${1:-${GENESISDB_VERSION:-latest}}"
PREFIX="${2:-${GENESISDB_PREFIX:-}}"

msg()  { printf "\033[1m==>\033[0m %s\n" "$*"; }
warn() { printf "\033[33mwarn:\033[0m %s\n" "$*" >&2; }
die()  { printf "\033[31merror:\033[0m %s\n" "$*" >&2; exit 1; }

command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar >/dev/null 2>&1 || die "tar is required"

CURL_AUTH=()
if [ -n "${GITHUB_TOKEN:-}" ]; then
  CURL_AUTH=(-H "Authorization: Bearer $GITHUB_TOKEN")
fi

case "$(uname -s)" in
  Linux) OS=linux ;;
  Darwin) OS=darwin ;;
  MINGW*|MSYS*|CYGWIN*) die "Windows detected; download the Windows zip from GitHub Releases" ;;
  *) die "unsupported operating system: $(uname -s)" ;;
esac

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) die "unsupported architecture: $(uname -m)" ;;
esac

if [ "$VERSION" = "latest" ]; then
  if [ ${#CURL_AUTH[@]} -gt 0 ]; then
    VERSION=$(curl -fsSL "${CURL_AUTH[@]+"${CURL_AUTH[@]}"}" \
      "https://api.github.com/repos/${OWNER}/${REPO}/releases/latest" \
      | sed -nE 's/.*"tag_name": *"([^"]+)".*/\1/p' | head -n1)
  else
    VERSION=$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
      "https://github.com/${OWNER}/${REPO}/releases/latest" \
      | sed -E 's|.*/tag/([^/]+).*|\1|')
  fi
  [ -n "$VERSION" ] || die "could not resolve the latest release"
fi
case "$VERSION" in v*) ;; *) VERSION="v$VERSION" ;; esac
VERSION_NUMBER="${VERSION#v}"

pick_prefix() {
  local candidates=()
  [ -n "$PREFIX" ] && { printf '%s\n' "$PREFIX"; return; }
  candidates+=("/usr/local/bin")
  [ -n "${HOME:-}" ] && candidates+=("$HOME/.local/bin" "$HOME/bin")
  for directory in "${candidates[@]}"; do
    if [ -d "$directory" ] && [ -w "$directory" ]; then
      printf '%s\n' "$directory"
      return
    fi
  done
  if [ -n "${HOME:-}" ]; then
    mkdir -p "$HOME/.local/bin"
    printf '%s\n' "$HOME/.local/bin"
    return
  fi
  die "no writable install directory found; pass one as the second argument"
}

PREFIX=$(pick_prefix)
mkdir -p "$PREFIX"

ARCHIVE="${BINARY}_${VERSION_NUMBER}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/${OWNER}/${REPO}/releases/download/${VERSION}"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

msg "downloading ${ARCHIVE}"
curl -fsSL "${CURL_AUTH[@]+"${CURL_AUTH[@]}"}" -o "$TMP/$ARCHIVE" "$BASE_URL/$ARCHIVE" \
  || die "download failed: $BASE_URL/$ARCHIVE"
curl -fsSL "${CURL_AUTH[@]+"${CURL_AUTH[@]}"}" -o "$TMP/checksums.txt" "$BASE_URL/checksums.txt" \
  || die "download failed: $BASE_URL/checksums.txt"

msg "verifying checksum"
expected=$(grep " ${ARCHIVE}\$" "$TMP/checksums.txt" | awk '{print $1}' || true)
[ -n "$expected" ] || die "no checksum found for $ARCHIVE"
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$TMP/$ARCHIVE" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$TMP/$ARCHIVE" | awk '{print $1}')
else
  die "sha256sum or shasum is required"
fi
[ "$expected" = "$actual" ] || die "checksum mismatch: expected $expected, got $actual"

msg "extracting"
tar -xzf "$TMP/$ARCHIVE" -C "$TMP"
[ -f "$TMP/$BINARY" ] || die "archive does not contain $BINARY"

msg "installing to $PREFIX/$BINARY"
install -m 0755 "$TMP/$BINARY" "$PREFIX/$BINARY" 2>/dev/null \
  || { cp "$TMP/$BINARY" "$PREFIX/$BINARY" && chmod 0755 "$PREFIX/$BINARY"; }

case ":$PATH:" in
  *":$PREFIX:"*) ;;
  *)
    warn "$PREFIX is not on your PATH"
    warn "add this to your shell configuration:"
    warn "  export PATH=\"$PREFIX:\$PATH\""
    ;;
esac

msg "installed $($PREFIX/$BINARY version 2>/dev/null || echo "$BINARY $VERSION_NUMBER")"
msg "run: genesisdb init"
