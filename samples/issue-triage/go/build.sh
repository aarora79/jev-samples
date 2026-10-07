#!/usr/bin/env bash
# Cross-compile issue-triage for every platform install.sh knows about,
# and write SHA256SUMS beside the binaries.
#
#   ./build.sh 0.1.0
#
# This sample is written in Go rather than ported to it, so there is no Python
# implementation to stay in step with. questions.yml one directory up is still
# the canonical payload, because that is where the other samples keep theirs and
# where a reader looks. go:embed cannot reach outside this folder, so this script
# copies that file in before building and payload_test.go fails if the copy ever
# drifts.
#
# Every binary is static (CGO_ENABLED=0), so it runs on a machine with no Go, no
# Python, and no libc of a particular vintage.
#
# Build from a clean clone when cutting a release. `go build` stamps
# vcs.revision and vcs.modified into every binary, so a release built from a
# dirty tree advertises modified=true and its hashes match nothing a reader can
# reproduce. `go version -m <binary>` prints the stamps.
set -euo pipefail

version="${1:-dev}"
here="$(cd "$(dirname "$0")" && pwd)"
canonical="$here/../questions.yml"
dist="$here/dist"

if ! cmp -s "$canonical" "$here/questions.yml"; then
  echo "syncing questions.yml from $canonical"
  cp "$canonical" "$here/questions.yml"
fi

gofmt -l "$here" | tee /tmp/issue-triage-gofmt
if [ -s /tmp/issue-triage-gofmt ]; then
  echo "gofmt found unformatted files above; run gofmt -w ." >&2
  exit 1
fi

go vet ./...
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
  name="issue-triage_${version}_${os}_${arch}"
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
