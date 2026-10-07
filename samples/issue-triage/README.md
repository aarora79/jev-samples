# issue-triage

Reads a repository's open issues and writes a markdown file saying which ones to work on today, this week, this month, and when time permits.

Two Jev calls per issue. The first asks eight questions about the issue text, the second reads a summary of those answers alongside the facts GitHub already knows and answers one question: when should somebody pick this up. On a 106-issue queue that is ten seconds and under a cent.

This sample is Go rather than Python, and it hand-rolls the Jev call as one `net/http` POST because there is no Go SDK. It ships as one static binary so a maintainer can run it against their own backlog without installing a runtime.

For the design and what it measured, read [explainer.md](explainer.md) beside this file.

## Run it

```bash
cd samples/issue-triage/go
go build -o issue-triage .

# Every open issue, into data/triage-owner-repo-open.md
./issue-triage agentic-community/mcp-gateway-registry

# A seeded sample of closed issues, as a sanity check against known outcomes
./issue-triage agentic-community/mcp-gateway-registry -state CLOSED -sample 25 -seed 7

# Everything behind one issue's bucket: both calls, and the exact digest
./issue-triage agentic-community/mcp-gateway-registry -explain 1575

# Print the report instead of writing it
./issue-triage owner/repo -stdout
```

It needs two credentials and no configuration file:

- **A GitHub token**, read from `GITHUB_TOKEN`, then `GH_TOKEN`, then `gh auth token`. GitHub's GraphQL API refuses anonymous callers, so there is no unauthenticated path.
- **A Jev key**, read from `TYPESAFE_API_KEY`, then from `.env` beside the binary or up to three directories above it. The environment wins, and the tool logs the path a key came from and never the key.

Both argument orders work: `issue-triage owner/repo -stdout` and `issue-triage -stdout owner/repo`. Go's flag package stops at the first positional argument, so the first form would otherwise drop the flag in silence.

## What it writes

One markdown file per repository and state, under `data/`. The summary table first:

```
| horizon           | issues | numbers                             |
| Today             | 4      | #1662, #1575, #1859, #1663          |
| This week         | 15     | #1571, #1607, #1597, #98, #895, ... |
| This month        | 67     | ...                                 |
| When time permits | 20     | ...                                 |
```

Then one table per bucket, where every row carries why it is there:

```
| issue  | kind   | area               | why it is here                        |
| #1575  | defect | deployment-and-ops | outside reporter commented once, no   |
|        |        |                    | reply, urgent in the body (1.79 of 2) |
```

That last column is what makes the tool correctable. A bucket you disagree with points at the signal that put it there, so the fix is a reworded question rather than a guess at a weight.

Two committed runs sit in `data/`, both from 7 October 2026 against `agentic-community/mcp-gateway-registry`: the 106 open issues, and a seeded sample of 25 closed ones.

## Install it as a Claude Code skill

One command puts the tool and the skill on the machine. After that the skill does the work, and nobody has to learn the flags.

```bash
curl -fsSL https://raw.githubusercontent.com/aarora79/jev-samples/main/samples/issue-triage/vend/install.sh | sh
```

That installs two things:

| What | Where | Why |
| --- | --- | --- |
| the `issue-triage` binary | `/usr/local/bin`, or `~/.local/bin` | reads the issues and sorts them, no runtime needed |
| `SKILL.md` | `~/.claude/skills/issue-triage/` | tells the agent when to triage and how to read the result |

Set `BINDIR` or `SKILL_DIR` to put either somewhere else, and `VERSION` to pin a release rather than take the newest. The installer checks both credentials and names the one you are missing rather than failing halfway through a run:

```bash
export GITHUB_TOKEN=...        # or GH_TOKEN, or run gh auth login
export TYPESAFE_API_KEY=...
```

Then start a new Claude Code session and ask in your own words:

> what should I work on in agentic-community/mcp-gateway-registry today?
>
> triage our open issues and tell me which ones are waiting on a reply from us
>
> sort the backlog on owner/repo into things for this week and things for later

The skill picks the flags, runs the binary, reads the report and tells you which issues to start on and why. When you disagree with a placement it runs `-explain` on that issue and shows the whole decision.

To read the installer before piping it into a shell:

```bash
curl -fsSLO https://raw.githubusercontent.com/aarora79/jev-samples/main/samples/issue-triage/vend/install.sh
less install.sh && sh install.sh
```

The binary on its own needs no skill and no install script: `cd go && go build -o issue-triage .` is the whole build.

## What to notice

**Every question asks something the text can settle.** `has_repro` asks whether the body carries steps, a command or a log. `claims_blocking` asks whether somebody says their work has stopped. A question like "how long will this take" has no answer in an issue body, so the payload does not contain one.

**Stage two never sees the body.** It reads a dozen lines of facts and nothing else, so an issue writing "CRITICAL, fix immediately" has to win on whether it carries a repro, whether anybody else turned up, and how long it has sat. Treat issue text as untrusted the way you would treat a prompt.

**The areas are coarse on purpose.** Six options, all externally visible: frontend, backend, infrastructure-as-code, deployment-and-ops, docs, unclear. An earlier version split along the codebase's own packages and scored 10% to 16% on the internal distinctions, because a reporter cannot tell one service package from another. The explainer has the table.

**Facts come from the API, never from a model.** Days open, days idle, whether a pull request is linked, who filed it, and who has been commenting are all exact and free. Asking a model to estimate any of them would be inventing a number that GitHub will hand over.

**The comment counts overlap rather than partition.** A maintainer who files an issue and then replies on it has written both a reporter comment and a maintainer comment. Counting it as only one of those made 18 issues read as unanswered outside reporters when the maintainer was talking on their own thread.

**The decision is a score, so the fraction orders the queue.** `work_horizon` has four levels and Jev returns a value between them, which sorts the issues inside each bucket. A pick-one question would throw that ordering away.

**Nothing here acts.** No labels, no comments, no closes, no assignments. TypeSafe publishes 67.8% accuracy on their own benchmark, September 2026 and unreproduced outside the company, which is enough to decide what to read first and not enough to decide anything else.

## Two experiments

**Reword an area and watch the buckets move.** Open `questions.yml`, rewrite one of the six `area` options in the words a reporter would use rather than the words the codebase uses, rebuild, and diff the committed report. Low agreement on an area usually means a description a body cannot decide, and two areas competing for the same words usually means one is stealing the other's answers.

**Drop a fact from the digest and see whether it mattered.** Comment out one line in `writeDiscussion` or one of the date facts in `buildDigest`, rerun, and diff. A fact that changes no bucket is costing tokens and can come out. This is cheaper than arguing about which signals matter: a full run is under a cent.

## Files

```
questions.yml   the canonical payload: the model pin, the state budgets,
                eight stage-one questions and the one stage-two decision
go/
  fetch.go      one GraphQL query per page, for the text and the facts
  payload.go    loading questions.yml, which build.sh embeds in the binary
  score.go      both calls, the digest between them, the bucketing after
  report.go     the markdown: summary table, a table per bucket, the reasons
  run.go        the key, seeded sampling, eight issues at a time
  main.go       flags and control flow
  payload_test.go  17 tests, including the embed drift check

  build.sh      five static targets plus SHA256SUMS
  install.sh    downloads the right release and checks its checksum
vend/           a SKILL.md and an installer, so any repo can use this
data/           the two committed runs
explainer.md    the design, what it measured, and what it cannot do
explainer.html  the same, as one self-contained page
```

`questions.yml` at the sample root is canonical, and `build.sh` copies it into `go/` before every build because Go's embed directive cannot reach outside its own directory. `payload_test.go` fails when the two drift apart.

## Checks

```bash
cd go
gofmt -l . && go vet ./... && go test ./...
./build.sh 0.1.0          # five targets, with checksums
```

A change to `questions.yml` means rerunning both committed reports and reading the diff, because the output is judged by reading it. Scores move slightly between runs of identical input, so re-run before believing a changed bucket: one rerun moved the Today count from 4 to 5.
