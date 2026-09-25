#!/usr/bin/env bash
# Cross-compile pr-triage for every platform install.sh knows about,
# and write SHA256SUMS beside the binaries.
#
#   ./build.sh 0.1.0
#
# The Python sample one directory up is the canonical implementation, and its
# questions.yml is the canonical payload. go:embed cannot reach outside this
# folder, so this script copies that file in before building, and
# payload_test.go fails if the copy ever drifts.
#
# Every binary is static (CGO_ENABLED=0), so it runs on a machine with no Go, no
# Python, and no libc of a particular vintage.
set -euo pipefail

version="${1:-dev}"
here="$(cd "$(dirname "$0")" && pwd)"
canonical="$here/../questions.yml"
dist="$here/dist"

if ! cmp -s "$canonical" "$here/questions.yml"; then
  echo "syncing questions.yml from the Python sample"
  cp "$canonical" "$here/questions.yml"
fi

go test ./...

rm -rf "$dist"
mkdir -p "$dist"

targets=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
  "windows amd64"
)

for target in "${targets[@]}"; do
  read -r os arch <<<"$target"
  name="pr-triage_${version}_${os}_${arch}"
  [ "$os" = "windows" ] && name="$name.exe"

  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build \
    -trimpath \
    -ldflags "-s -w -X main.version=$version" \
    -o "$dist/$name" \
    "$here"
  echo "built $name"
done

cd "$dist"
sha256sum ./* > SHA256SUMS
echo
cat SHA256SUMS
