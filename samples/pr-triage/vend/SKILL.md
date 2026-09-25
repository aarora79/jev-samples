---
name: pr-triage
description: Sort a repository's open pull requests into review tiers, so a maintainer knows which ones a glance clears and which ones need an hour. Use when asked to triage, prioritise, or plan a review queue, to say which pull requests need attention first, or to gate a branch on how much review its queue is carrying. Works against github.com and GitHub Enterprise Server.
license: Apache-2.0
metadata:
  author: aarora79
  requires: pr-triage binary, a GitHub token, a TypeSafe API key
---

# pr-triage

Ask one question per pull request that a reviewer actually has: how much review does this need? The `pr-triage` binary fetches a repository's pull requests, asks Jev eleven questions about each one in a single call, and sorts them into four tiers with the advice for each.

One pull request costs about a third of a cent to read at 2026 prices, and a queue of twenty-six takes about four seconds.

## Before you run it

The binary needs two credentials, and reads each from a flag first and the environment second.

| What | Flag | Environment |
| --- | --- | --- |
| GitHub | `-token` | `GITHUB_TOKEN`, `GH_TOKEN`, `GH_ENTERPRISE_TOKEN`, or a logged-in `gh` CLI |
| TypeSafe | `-jev-key` | `TYPESAFE_API_KEY` |

Check the binary is there, and install it if it is not:

```bash
command -v pr-triage || curl -fsSL https://raw.githubusercontent.com/aarora79/jev-samples/main/samples/pr-triage/go/install.sh | sh
```

If `TYPESAFE_API_KEY` is unset, ask the user for it rather than guessing. Never print it back.

## Running it

```bash
# The ten most recent open pull requests
pr-triage owner/repo

# Every open one, or a window
pr-triage owner/repo -all
pr-triage owner/repo -since 30
pr-triage owner/repo -since 2026-08-01

# One pull request, every question and what each contributed
pr-triage owner/repo -explain 1693

# Build the dataset now, triage it later for the cost of the Jev calls alone
pr-triage owner/repo -all -fetch-only
pr-triage -dataset data/owner-repo-open-all.json
```

On GitHub Enterprise Server, pass a URL on the host and the API base follows it:

```bash
pr-triage https://ghe.example.com/owner/repo
pr-triage owner/repo -api-base https://ghe.example.com/api/v3
```

Add `-quiet` when you are going to read the output rather than show it, which drops the progress lines and leaves the report on standard output.

## Reading what comes back

Every run prints a table, then the same pull requests grouped by tier with the advice for each, then what the run cost. It also writes two files into `-out` (default `./data`): a JSON report holding every answer, and a markdown report that pastes into an issue or a pull request without reformatting.

Four tiers, cheapest review first:

| Tier | What to do |
| --- | --- |
| `trivial` | merge on a glance: title, skim, green CI |
| `low` | one reviewer, one pass, no meeting |
| `medium` | one reviewer who knows the area, reading the whole diff |
| `high` | a human reads it line by line, and the author walks them through it |

Three things in that output need reading with care.

**A tier marked `(size)` came from the file and line counts, not from Jev.** Jev reads a truncated diff, so a 54-file change can read as one repeated edit. The size floor raises a tier and never lowers one, and the mark says where it did.

**A tier marked `(on a cut)` sits within 0.02 of a band edge.** Repeat calls move a load by about that much, so it could land either side on the next run. Treat it as either of the two tiers it straddles.

**A line saying it read part of the diff means the patch overflowed the budget.** A load drawn from 44% of a change is a weaker claim than the same number drawn from all of it.

## Gating a branch

`-fail-on-tier` exits 2 when any pull request lands in that tier or above, which turns the triage into a check:

```bash
pr-triage owner/repo -all -fail-on-tier high -quiet
```

Exit codes: 0 finished clean, 1 could not finish, 2 a gate fired.

## What this does not do

It reads the pull request, and nothing else. It has no view of the repository around the diff, so it cannot tell whether a command the description names really exists, and it never says a change is correct. Nothing here approves, merges or comments.

The tiers are advice about where to spend attention. TypeSafe reports 67.8% accuracy on their own benchmark, published September 2026, so before anyone trusts a tier, point it at pull requests your team already reviewed and compare the tiers against what those reviews cost. Move the cuts until they match the repository.
