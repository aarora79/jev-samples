---
name: pr-triage
description: Say what evidence each open pull request needs before it merges, routing each one to a green tick, a test, an AI review, or a person. Use when asked to triage, prioritise, or plan a review queue, to say which pull requests need a human and which clear on CI, to work out where to start reading, or to gate a branch on how much review its queue is carrying. Works against github.com and GitHub Enterprise Server.
license: Apache-2.0
metadata:
  author: aarora79
  requires: pr-triage binary, a GitHub token, a TypeSafe API key
---

# pr-triage

Answer the question a reviewer actually has: what does this change need before it can merge? The `pr-triage` binary fetches a repository's pull requests, asks Jev eleven questions about each one in a single call, and routes each to the cheapest evidence that would settle it, from a green tick up to a person reading it with the author.

One pull request costs about two hundredths of a cent at 2026 prices. Seven hundred and ten of them cost $0.14 and two minutes.

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

Each pull request gets a **route**, naming what evidence is sufficient before it merges, cheapest first:

| Route | Sufficient evidence |
| --- | --- |
| `green-is-enough` | CI passing |
| `tests-are-enough` | CI passing, and a test that exercises the change |
| `ai-review-is-enough` | the above, and an AI review that finds nothing |
| `human-required` | a person reads it, whatever the machines say |
| `human-plus-author` | a person reads it line by line, with the author walking them through |

The route comes from two numbers: **consequence**, the max of the four questions about what breaks if this is wrong, and **effort**, the weighted mean of nine. Consequence sets the floor and effort can only raise it. Nothing here says merge.

Three things in that output need reading with care.

**A number marked `(size)` came from the file and line counts, not from Jev.** Jev reads a truncated diff, so a 54-file change can read as one repeated edit. The size floor raises a tier and never lowers one, and the mark says where it did.

**Anything marked `(on a cut)` sits within 0.02 of a threshold.** Repeat calls move a load by about 0.03 and a consequence by about 0.07, so it could land either side next run. Treat it as either of the two it straddles.

**A line saying it read part of the diff means the patch overflowed the budget.** A load drawn from 44% of a change is a weaker claim than the same number drawn from all of it.

## Gating a branch

`-fail-on-route` exits 2 when any pull request needs that route or a costlier one, which turns the triage into a check:

```bash
pr-triage owner/repo -all -fail-on-route human-required -quiet
```

Exit codes: 0 finished clean, 1 could not finish, 2 a gate fired.

## What this does not do

It reads the pull request, and nothing else. It has no view of the repository around the diff, so it cannot tell whether a command the description names really exists, and it never says a change is correct. Nothing here approves, merges or comments.

The routes are advice about how much evidence to demand. TypeSafe reports 67.8% accuracy on their own benchmark, published September 2026, so before anyone trusts a route, point it at pull requests your team already merged and check how many the tool would have sent to `green-is-enough` that later needed a revert. Move the cuts until that count is zero.
