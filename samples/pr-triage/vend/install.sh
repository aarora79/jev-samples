#!/bin/sh
# Install the pr-triage skill and the binary it runs.
#
#   curl -fsSL https://raw.githubusercontent.com/aarora79/jev-samples/main/samples/pr-triage/vend/install.sh | sh
#
# This puts two things on the machine: the pr-triage binary, which fetches pull
# requests and triages them, and a SKILL.md that tells an agent when and how to
# run it. Neither needs Python, Go, or a checkout of this repo.
#
# Environment:
#   VERSION    the release to install, for example 0.1.0, default the newest
#   BINDIR     where the binary goes, default /usr/local/bin then ~/.local/bin
#   SKILL_DIR  where the skill goes, default ~/.claude/skills/pr-triage
#
# Piping a script from the internet into a shell is a choice. To read it first:
#   curl -fsSLO https://raw.githubusercontent.com/aarora79/jev-samples/main/samples/pr-triage/vend/install.sh
#   less install.sh && sh install.sh
set -eu

REPO="aarora79/jev-samples"
RAW="https://raw.githubusercontent.com/$REPO/main/samples/pr-triage"
BINARY="pr-triage"
SKILL_DIR="${SKILL_DIR:-$HOME/.claude/skills/pr-triage}"

fail() {
  echo "install: $1" >&2
  exit 1
}

command -v curl >/dev/null 2>&1 || fail "curl is required"

# The binary first, because a skill with nothing to run is worse than no skill.
# go/install.sh resolves the newest pr-triage release, checks the published
# SHA256SUMS, and picks the build for this platform. VERSION and BINDIR pass
# straight through to it.
if command -v "$BINARY" >/dev/null 2>&1; then
  echo "found $BINARY on PATH: $($BINARY -version)"
else
  echo "installing the $BINARY binary"
  curl -fsSL "$RAW/go/install.sh" | sh
fi

# Then the skill. One file, so a plain download is the whole install.
echo "installing the skill into $SKILL_DIR"
mkdir -p "$SKILL_DIR"
curl -fsSL "$RAW/vend/SKILL.md" -o "$SKILL_DIR/SKILL.md" ||
  fail "could not download SKILL.md from $RAW/vend/SKILL.md"

echo
echo "installed:"
echo "  skill   $SKILL_DIR/SKILL.md"
printf '  binary  '
command -v "$BINARY" || echo "not on PATH yet, see the note above"

# The skill cannot work without these, and finding out now beats finding out
# halfway through a triage.
echo
missing=""
[ -n "${TYPESAFE_API_KEY:-}" ] || missing="TYPESAFE_API_KEY"
if [ -z "${GITHUB_TOKEN:-}${GH_TOKEN:-}${GH_ENTERPRISE_TOKEN:-}" ] &&
  ! gh auth token >/dev/null 2>&1; then
  missing="$missing GITHUB_TOKEN"
fi

if [ -n "$missing" ]; then
  echo "still needed before the first run:$missing" >&2
  echo "  GitHub   GITHUB_TOKEN, GH_TOKEN, GH_ENTERPRISE_TOKEN, or gh auth login" >&2
  echo "  TypeSafe TYPESAFE_API_KEY" >&2
else
  echo "credentials found for GitHub and TypeSafe"
fi

echo
echo "try it:  $BINARY owner/repo"
echo "on GHES: $BINARY https://ghe.example.com/owner/repo"
