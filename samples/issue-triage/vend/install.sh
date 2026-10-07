#!/bin/sh
# Install the issue-triage skill and the binary it runs.
#
#   curl -fsSL https://raw.githubusercontent.com/aarora79/jev-samples/main/samples/issue-triage/vend/install.sh | sh
#
# This puts two things on the machine: the issue-triage binary, which reads a
# repository's issues and sorts them into four buckets, and a SKILL.md that tells
# an agent when and how to run it. Neither needs Python, Go, or a checkout of
# this repo.
#
# Environment:
#   VERSION    the release to install, for example 0.1.0, default the newest
#   BINDIR     where the binary goes, default /usr/local/bin then ~/.local/bin
#   SKILL_DIR  where the skill goes, default ~/.claude/skills/issue-triage
#
# Piping a script from the internet into a shell is a choice. To read it first:
#   curl -fsSLO https://raw.githubusercontent.com/aarora79/jev-samples/main/samples/issue-triage/vend/install.sh
#   less install.sh && sh install.sh
set -eu

REPO="aarora79/jev-samples"
RAW="https://raw.githubusercontent.com/$REPO/main/samples/issue-triage"
BINARY="issue-triage"
SKILL_DIR="${SKILL_DIR:-$HOME/.claude/skills/issue-triage}"

fail() {
  echo "install: $1" >&2
  exit 1
}

command -v curl >/dev/null 2>&1 || fail "curl is required"

# The binary first, because a skill with nothing to run is worse than no skill.
# go/install.sh resolves the newest issue-triage release, checks the published
# SHA256SUMS, and picks the build for this platform. VERSION and BINDIR pass
# straight through to it.
#
# This installs even when a binary is already on PATH, so re-running the script
# never leaves an old binary beside a skill that expects the current one. Pin
# with VERSION for a specific release.
if command -v "$BINARY" >/dev/null 2>&1; then
  echo "replacing the $BINARY already on PATH: $($BINARY -version)"
else
  echo "installing the $BINARY binary"
fi
curl -fsSL "$RAW/go/install.sh" | sh

# Then the skill. One file, so a plain download is the whole install.
echo "installing the skill into $SKILL_DIR"
mkdir -p "$SKILL_DIR"
curl -fsSL "$RAW/vend/SKILL.md" -o "$SKILL_DIR/SKILL.md" ||
  fail "could not download SKILL.md from $RAW/vend/SKILL.md"

echo
echo "installed:"
echo "  skill   $SKILL_DIR/SKILL.md"
# The binary's path comes from go/install.sh above, which prints where it landed
# and warns when that is off PATH. Reporting it again here would read it back off
# PATH, which names a different binary whenever BINDIR is somewhere else.
echo "  binary  see the path printed above"

# The skill cannot work without these, and finding out now beats finding out
# halfway through a triage.
#
# Each credential gets its own flag, so the report names only what is actually
# missing rather than sending somebody looking for a GITHUB_TOKEN they already
# have through a logged-in gh.
echo
need_github=""
need_typesafe=""

if [ -z "${GITHUB_TOKEN:-}${GH_TOKEN:-}" ] && ! gh auth token >/dev/null 2>&1; then
  need_github="yes"
fi
[ -n "${TYPESAFE_API_KEY:-}" ] || need_typesafe="yes"

if [ -n "$need_github" ] || [ -n "$need_typesafe" ]; then
  echo "still needed before the first run:" >&2
  [ -z "$need_github" ] ||
    echo "  GitHub    GITHUB_TOKEN, GH_TOKEN, or gh auth login" >&2
  [ -z "$need_typesafe" ] ||
    echo "  TypeSafe  TYPESAFE_API_KEY" >&2
else
  echo "credentials found for GitHub and TypeSafe"
fi

echo
echo "try it:   $BINARY owner/repo"
echo "one issue: $BINARY owner/repo -explain 1575"
