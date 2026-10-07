---
name: issue-triage
description: Say which open issues to work on today, this week, this month, and when time permits. Use when asked to triage, prioritise or sort an issue backlog, to decide what to pick up next, to find the quick wins or the stale items in a queue, to work out which issues are waiting on a maintainer reply, or to produce a standup or planning list from open issues.
license: Apache-2.0
metadata:
  author: aarora79
  requires: issue-triage binary, a GitHub token, a TypeSafe API key
---

# issue-triage

Answer the question somebody has in front of a backlog: what should I work on today? The `issue-triage` binary reads a repository's open issues, asks Jev eight questions about each issue's text, hands those answers plus the facts GitHub already knows to a second call, and groups the result into four buckets: today, this week, this month, when time permits.

A 106-issue queue costs $0.0093 and ten seconds, measured 7 October 2026.

## Before you run it

The binary needs two credentials and no configuration file.

| What | Where it reads from |
| --- | --- |
| GitHub | `GITHUB_TOKEN`, `GH_TOKEN`, or a logged-in `gh` CLI |
| TypeSafe | `TYPESAFE_API_KEY`, or a `.env` file beside the binary or above it |

Check the binary is there, and install it if it is not:

```bash
command -v issue-triage || curl -fsSL https://raw.githubusercontent.com/aarora79/jev-samples/main/samples/issue-triage/go/install.sh | sh
```

If `TYPESAFE_API_KEY` is unset, ask the user for it rather than guessing. Never print it back.

## When the request names no repository

Ask before running anything. If the working directory is a git checkout with a GitHub remote, offer that repository first, by name, as the likely answer. Offer it rather than assume it: a request to triage issues is often about a different repository than the one open in the terminal.

Then run the whole open queue unless the user says otherwise. The cost is under a cent, so reading all of it is cheaper than a conversation about how much to read.

## What they ask for, and what you run

| They say | You run | Means |
| --- | --- | --- |
| nothing, or "the backlog" | `issue-triage owner/repo` | every open issue, the default |
| "just the recent ones" | `-limit 30` | the 30 most recently updated |
| "what about the closed ones" | `-state CLOSED` | closed instead of open |
| "check it against what we fixed" | `-state CLOSED -sample 25 -seed 7` | a repeatable random sample |
| "why is #1575 in there" | `-explain 1575` | both calls and the exact fact table |
| "just show me, do not write a file" | `-stdout` | the report on standard output |

`-explain` reads an issue out of the run it just did, so the number has to fall inside the state and the limit. Explaining a closed issue needs `-state CLOSED` as well.

There is no selector for "these three issues". Run the queue and report those rows.

## Running it

```bash
# The whole open queue, written to data/triage-owner-repo-open.md
issue-triage owner/repo

# Print it instead of writing a file, which is what you want when you are
# going to summarise it in a reply rather than hand over a path
issue-triage owner/repo -stdout

# The most recently updated 30, for a quick look at a large backlog
issue-triage owner/repo -limit 30

# Everything behind one issue's bucket: both calls and the exact fact table
issue-triage owner/repo -explain 1575

# A seeded sample of closed issues, to sanity-check the buckets against
# work whose outcome is already known
issue-triage owner/repo -state CLOSED -sample 25 -seed 7
```

Either argument order works, so `issue-triage -stdout owner/repo` is fine too.

## Reading the output

A summary table of the four buckets, then one table per bucket where every row carries why it is there. Report the buckets and the reasons, not just the numbers: the reasons are what let the user disagree with a placement.

Three things in the output deserve passing on rather than summarising away.

**A row saying a pull request is already open.** That issue needs finishing rather than starting, and it is the most actionable thing in the report.

**A row saying an outside reporter commented with no maintainer reply.** Somebody is waiting, and a reply costs a minute. Say how many such issues there are even when none reached the top bucket.

**A row marked as sitting on a bucket edge.** That score is close enough to a cut that a rerun may move it, so do not build an argument on its exact position.

## Reporting it back

**Every issue number you show the user is a clickable link.** The point of a triage is that somebody opens the issues it names, and a bare `#1575` makes them go and search for it.

The markdown report already writes them as links in its per-bucket tables, so copy those rather than rebuilding them. The summary table prints bare numbers on purpose, so they stay readable in a terminal: add the links yourself when you relay that table.

Build the address from the repository you were given, never from a guess. An issue lives at `https://github.com/<owner>/<repo>/issues/<number>`.

Report all four buckets, not only the top one. A queue where sixty issues land in "this month" is telling the user something about the shape of their backlog, and a summary naming only the four for today hides it.

## What to tell the user about the limits

Say these when handing over a first run, because a reader who discovers them later stops trusting the rest.

The buckets are advice from a model, and nothing in the tool labels, closes, comments on or assigns anything. TypeSafe publishes 67.8% accuracy on their own benchmark, their figure from September 2026, unreproduced outside the company.

Scores move slightly between runs, so the count in one bucket can change by one or two with no input changing.

The two top buckets are capped, at five and fifteen. An issue that scored into a full bucket moves down one and its row says so, which means "this week" is not strictly everything the model thought was a this-week job.

Before anybody leans on the buckets, point the tool at issues the team has already closed:

```bash
issue-triage owner/repo -state CLOSED -sample 25 -seed 7
```

Then read the top bucket against what the team remembers mattering. Issues they recall as urgent that land in "when time permits" are the failure worth finding, and the fix is a reworded question rather than a new threshold. Closed issues carry a large "days since last activity", which pushes them down, so that check reads fairly at the top of the ranking and unfairly at the bottom.

## When the user disagrees with a placement

This is the useful loop, and it is cheap. Run `-explain <number>` on the issue in question, which prints every stage-one answer, the exact fact table the decision read, and the score that came back. Then one of three things is true:

- A stage-one answer is wrong about the text. The question wording is the fix, in `questions.yml`.
- The facts are right and the decision disagrees with the user. The decision wording or the horizon levels are the fix.
- Two questions are competing for the same words, which shows up as one firing where the other should.

Rerunning after a change costs under a cent, so test a rewrite rather than argue about one.

## Do not

Do not hand the markdown file over as the whole answer when the user asked a question. Read it and answer them, then point at the file.

Do not describe the output as a priority score or an effort estimate. It is a time horizon, and the tool makes no claim about how long anything will take.

Do not run it on a repository the user has not named or confirmed.
