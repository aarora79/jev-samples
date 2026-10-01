---
name: pr-triage
description: Say what evidence each open pull request needs before it merges, routing each one to a green tick, a test, an AI review, or a person. Use when asked to triage, prioritise, or plan a review queue, to say which pull requests need a human and which clear on CI, to work out where to start reading, or to gate a branch on how much review its queue is carrying. Works against github.com and GitHub Enterprise Server.
license: Apache-2.0
metadata:
  author: aarora79
  requires: pr-triage binary, a GitHub token, a TypeSafe API key
---

# pr-triage

Answer the question a reviewer has in front of a queue: what does this change need before it can merge? The `pr-triage` binary fetches a repository's pull requests, asks Jev twelve questions about each one in a single call, and routes each to the cheapest evidence that would settle it, from a green tick up to a person reading it with the author.

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

## When the request names no repository

Ask before running anything. Two things decide the whole run, and guessing either one reads the wrong queue or spends calls the user did not want:

**Which repository.** If the working directory is a git checkout with a GitHub remote, offer that first, named, as the likely answer. Offer it, do not assume it: a request to triage pull requests is often about a different repository than the one open in the terminal. If that repository has no open pull requests, say so and ask which one they meant rather than picking another.

**How much of the queue.** Say what forms the answer can take, because the user cannot guess them. Any of these works, and they combine:

| They say | You run | Means |
| --- | --- | --- |
| nothing, or "the recent ones" | `pr-triage owner/repo` | the 25 most recent open, the default |
| "the last 25" | `-limit 25` | the most recent N |
| "everything open" | `-all` | every open one, however many |
| "the last 30 days" | `-since 30` | a number of days back |
| "since August" | `-since 2026-08-01` | on or after a date, `YYYY-MM-DD` |
| "just this one" plus a link | `pr-triage https://github.com/owner/repo/pull/1803` | that pull request, fetched directly |
| "why did #1803 come out like that" | `-explain 1803` | every question and what each contributed |
| "the ones we merged last month" | `-state closed -since 30` | closed instead of open, for checking the routes against history |

A pasted pull request URL works as the whole argument, so a user who drops a link into the conversation needs nothing else. A list of several numbers has no flag: triage the repository with a selector wide enough to contain them, then report those rows.

`-explain` reads a pull request out of the run it just did, so the number has to fall inside the selector. `-explain 1803` on a default run of twenty-five prints that it is not in the dataset when the number is older than that. Pass the pull request's own URL, or widen the selector until it is included.

The default stops at twenty-five because fetching one pull request costs seven GitHub calls. The cost is invisible on a default run and real on `-all` against a repository with hundreds open, where the authenticated limit of 5,000 calls an hour caps one run at roughly seven hundred pull requests. Use `-fetch-only` once and then `-dataset` to re-triage without paying for the fetch again.

Say what it will cost before running: about two hundredths of a cent per pull request that reaches Jev, and nothing for the ones pre-triage stops. A few hundred is cheap and fast, so `-all` is a reasonable answer on most repositories. Warn first when the count runs to thousands.

## Running it

```bash
# The 25 most recent open pull requests
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

Every run prints a summary, a short list of what to clear first, two more tables, then the same pull requests grouped by route with the advice for each, then what the run cost. It also writes two files into `-out` (default `./data`): a JSON report holding every answer, and a markdown report that pastes into an issue or a pull request without reformatting.

The first table is the summary: one row per outcome, with a count and the numbers in it. It covers the whole queue, so the pre-triage states are rows alongside the routes and the counts add up to what was fetched.

**Start here answers what to do this morning, and it is the part to lead with.** Three pull requests, cheapest evidence first and smallest diff inside that, so a green tick comes before a test and a test before an AI review:

```text
1. #73722  green-is-enough  1 file, +2/-1  merge when CI is green: nothing here needs a person
   Clarify max_db_retries doc wording to avoid off-by-one confusion
2. #73729  tests-are-enough  2 files, +51/-2  merge when CI is green and a test exercises the change
   Fix clearing with upstream and downstream selecting unrelated tasks
```

When nothing on the queue clears without a person, the list falls back to the shortest human reads and says so. Either way these three are the answer to "what should I work on", so give them before the counts when somebody asks that. The same picks land in the JSON report as `start_here`, in order.

The next table answers a different question: who owes a second look. One row per reviewer who asked for changes on a pull request whose author has pushed since, oldest first. That pull request is back in the reviewer's court, and no route can move it, so pass the row to the person named rather than reading it as work for whoever asked you.

```text
| Reviewer | Waiting | Pull requests       |
| -------- | ------- | ------------------- |
| omrishiv | 3       | #1678, #1764, #1783 |
```

The same grouping lands in the JSON report as `awaiting_reviewer`, keyed by login. A line under the table names any of those that cannot merge whatever the reviewer decides, being held up by a pre-triage state as well.

The last table carries one row per pull request that reached Jev: number, title, size, effort, consequence, the question that produced the consequence, and the route it bought.

Four states come before a route. Rules settle three of them at no cost, and a pull request in one never reaches Jev:

| State | Means | Waiting on |
| --- | --- | --- |
| `draft` | the author marked it draft | the author, who is not asking |
| `pending-author-rework` | a reviewer asked for changes, or asked for something in a comment, and the author has not pushed since | the author, who has not answered |
| `ci-failing` | a check failed, timed out, or wants action | the checks, or the branch |
| `ci-pending` | a check is still running | nobody, come back later |

`pending-author-rework` has a second path that does cost a call. When a reviewer asks for something in a comment rather than a formal review, nothing appears in the reviews API, so Jev reads the comment and the pull request moves after the call instead of before it. Those rows carry the route they earned, printed as `once answered it needs: ...`, so you can say what the change will need once its author replies.

Report these, and do not read them as a backlog of review work. A queue that is mostly `pending-author-rework` or `ci-failing` is waiting on its authors, so telling the user to go and review those wastes their time. Pushing a commit clears it, with nobody dismissing anything.

Everything else gets a **route**, answering one question: what would be enough to merge this change? Cheapest first:

| Route | Enough to merge on |
| --- | --- |
| `green-is-enough` | CI passing |
| `tests-are-enough` | CI passing, and a test that exercises the change |
| `ai-review-is-enough` | the above, and an AI review that finds nothing |
| `human-required` | a person reads it, whatever the machines say |
| `human-plus-author` | a person reads it line by line, with the author walking them through |

The route comes from two numbers: **consequence**, the max of the four questions about what breaks if this is wrong, and **effort**, the weighted mean of nine. Consequence sets the floor and effort can only raise it. None of these says merge it now, and the tool never merges anything: they name what a merge would need.

Three things in that output need reading with care.

**A number marked `(size)` came from the file and line counts, not from Jev.** Jev reads a truncated diff, so a 54-file change can read as one repeated edit. The size floor raises a tier and never lowers one, and the mark says where it did.

**Anything marked `(on a cut)` sits within 0.02 of a threshold.** Repeat calls move a load by about 0.03 and a consequence by about 0.07, so it could land either side next run. Treat it as either of the two it straddles.

**A line saying it read part of the diff means the patch overflowed the budget.** A load drawn from 44% of a change is a weaker claim than the same number drawn from all of it.

## Reporting it back

**Every pull request number you show the user is a clickable link.** The point of a triage is that somebody opens the pull requests it names, and a bare `#1693` makes them search for it. Write each one as a markdown link:

```markdown
| human-required | 9 | [#1795](https://github.com/owner/repo/pull/1795), [#1794](...) |
```

Take the address from the `url` field the JSON report carries for every pull request, in `pull_requests[]` and in `not_reviewable[]`. Never assemble a `github.com` URL from the number, because a GitHub Enterprise Server host has a different one and the report already knows it.

The binary's own tables print bare numbers on purpose, so they stay readable in a terminal and line up as padded markdown. Adding the links is your job when you relay the result. The grouped sections lower down in the markdown report already carry linked numbers, so those can be copied as they are.

Report the whole queue, states included. A run where most of the queue is `ci-failing` is saying that the checks are the bottleneck, and a summary listing only the routes hides that.

## Gating a branch

`-fail-on-route` exits 2 when any pull request needs that route or a costlier one, which turns the triage into a check:

```bash
pr-triage owner/repo -all -fail-on-route human-required -quiet
```

Exit codes: 0 finished clean, 1 could not finish, 2 a gate fired.

## What this does not do

It reads the pull request, and nothing else. It has no view of the repository around the diff, so it cannot tell whether a command the description names really exists, and it never says a change is correct. Nothing here approves, merges or comments.

The routes are advice about how much evidence to demand. TypeSafe reports 67.8% accuracy on their own benchmark, published September 2026, so before anyone trusts a route, point it at pull requests your team already merged and check how many the tool would have sent to `green-is-enough` that later needed a revert. Move the cuts until that count is zero.
