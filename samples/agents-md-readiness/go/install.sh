#!/bin/sh
# Install agents-md-readiness: one static binary, no Go and no Python needed.
#
#   curl -fsSL https://raw.githubusercontent.com/aarora79/jev-samples/main/samples/agents-md-readiness/go/install.sh | sh
#
# Environment:
#   VERSION  semver release to install, for example 0.1.0, default the newest
#   BINDIR   where to put the binary, default /usr/local/bin then ~/.local/bin
#
# Piping a script from the internet into a shell is a choice. To read it first:
#   curl -fsSLO https://raw.githubusercontent.com/aarora79/jev-samples/main/samples/agents-md-readiness/go/install.sh
#   less install.sh && sh install.sh
set -eu

REPO="aarora79/jev-samples"
TAG_PREFIX="agents-md-readiness/"
BINARY="agents-md-readiness"

fail() {
  echo "install: $1" >&2
  exit 1
}

# resolve_version reads the newest tag carrying the tool's prefix.
resolve_version() {
  curl -fsSL "https://api.github.com/repos/$REPO/releases" |
    grep '"tag_name":' |
    sed -e 's/.*"tag_name": *"//' -e 's/".*//' |
    grep "^$TAG_PREFIX" |
    head -n 1 |
    sed -e "s|^$TAG_PREFIX||"
}

platform() {
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  case "$os" in
    linux | darwin) ;;
    *) fail "no prebuilt binary for $os. Build it with go build, or see go/README.md" ;;
  esac

  arch=$(uname -m)
  case "$arch" in
    x86_64 | amd64) arch="amd64" ;;
    aarch64 | arm64) arch="arm64" ;;
    *) fail "no prebuilt binary for $arch. Build it with go build, or see go/README.md" ;;
  esac

  echo "${os}_${arch}"
}

checksum() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | cut -d' ' -f1
  else
    echo ""
  fi
}

command -v curl >/dev/null 2>&1 || fail "curl is required"

version="${VERSION:-}"
[ -n "$version" ] || version=$(resolve_version)
[ -n "$version" ] || fail "no $TAG_PREFIX release found on $REPO"
# Releases are plain semver, so drop a leading v if someone typed one.
version="${version#v}"

target=$(platform)
asset="${BINARY}_${version}_${target}"
base="https://github.com/$REPO/releases/download/${TAG_PREFIX}${version}"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "downloading $asset ($version)"
curl -fsSL "$base/$asset" -o "$tmp/$BINARY" || fail "could not download $base/$asset"

if curl -fsSL "$base/SHA256SUMS" -o "$tmp/SHA256SUMS" 2>/dev/null; then
  want=$(grep "$asset\$" "$tmp/SHA256SUMS" | cut -d' ' -f1)
  got=$(checksum "$tmp/$BINARY")
  if [ -z "$got" ]; then
    echo "install: no sha256 tool found, skipping the checksum" >&2
  elif [ "$want" != "$got" ]; then
    fail "checksum mismatch for $asset: expected $want, got $got"
  else
    echo "checksum ok"
  fi
else
  echo "install: no SHA256SUMS published for $version, skipping the checksum" >&2
fi

bindir="${BINDIR:-}"
if [ -z "$bindir" ]; then
  if [ -w /usr/local/bin ]; then
    bindir="/usr/local/bin"
  else
    bindir="$HOME/.local/bin"
  fi
fi
mkdir -p "$bindir"

install -m 0755 "$tmp/$BINARY" "$bindir/$BINARY" 2>/dev/null ||
  { cp "$tmp/$BINARY" "$bindir/$BINARY" && chmod 0755 "$bindir/$BINARY"; }

echo "installed $bindir/$BINARY"
"$bindir/$BINARY" -version

case ":$PATH:" in
  *":$bindir:"*) ;;
  *) echo "note: $bindir is not on your PATH" >&2 ;;
esac
