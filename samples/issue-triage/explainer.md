# Sorting a 106-issue backlog in ten seconds

*How issue-triage decides what to work on today, why it asks the questions it asks, and what it produced on a real queue. Measured 7 October 2026 against `agentic-community/mcp-gateway-registry`.*

| figure | what it counts |
|---|---|
| 106 | open issues, 57 older than 90 days, 54 with no label |
| 10 seconds | to read and sort all of them |
| $0.0093 | the whole run, 221,768 input tokens |
| 4 | issues it says to start on now |

## Start with a question nobody can answer

Issue #1832 says the registry's outbound HTTP client ignores `HTTPS_PROXY`, so the health checker cannot reach servers behind a corporate proxy. Ask how long it will take to fix. The answer is not in the issue, the reporter does not know it, and careful reading will not produce it: the body describes a symptom and the cost lives in code the reporter has never opened. Its closing pull request changed 39 files.

Now ask a different question about the same text. Is somebody blocked? Does the body contain a way to reproduce it? Does it propose a fix? Is this a defect or a feature request?

Every one of those is a fact about the text in front of you. The tool asks only questions a body can settle, and takes everything else from the GitHub API, which knows the answers exactly.

## What Jev is, and what it hands back

Jev is a small model from TypeSafe that answers a fixed list of questions about one piece of text. You send the text once and every question you want asked about it, and you get one typed answer per question. No prose comes back, so there is nothing to parse and nothing to retry when the parse fails.

Questions come in three shapes, and this tool uses each of them.

| shape | asks | hands back |
|---|---|---|
| `Noul` | one yes-or-no claim | a probability from 0 to 1 |
| `Choice` | pick one of a named set | the option, its confidence, and the spread over all options |
| `Score` | where this sits on a rubric | a number between levels, with the level text and the spread |

Here is a real call, cut down to three questions so it fits on a page. The request:

```json
{
  "model": "jev-1.13.0",
  "state": {
    "issue_body": "Calling my registered server through the gateway returns 502
      after about 30 seconds. Steps: register a server with a 60s startup, then
      GET /mcpgw/my-server. Logs attached. We cannot ship until this is fixed."
  },
  "questions": {
    "has_repro": {
      "type": "noul",
      "instructions": "The body carries steps, a command, a log, an error
        message or a payload that shows the problem happening"
    },
    "report_kind": {
      "type": "choice",
      "instructions": "What kind of report this is",
      "criteria": {
        "defect": "Something is broken now",
        "feature-request": "A capability that does not exist yet",
        "question": "A question rather than a request"
      }
    },
    "urgency_stated": {
      "type": "score",
      "instructions": "How urgently the body says this is needed",
      "criteria": ["Nice to have, someday", "Wanted",
                   "Cannot proceed without it"]
    }
  }
}
```

And the answer, 469 input tokens:

```json
{
  "answers": {
    "has_repro":   { "type": "noul", "noul": 0.84 },
    "report_kind": { "type": "choice", "choice": "defect", "confidence": 1.0,
                     "probabilities": {"defect": 1.0, "feature-request": 0.0,
                                       "question": 0.0} },
    "urgency_stated": { "type": "score", "score": 2.0, "confidence": 1.0,
                        "legend": {"0": "Nice to have, someday",
                                   "1": "Wanted",
                                   "2": "Cannot proceed without it"},
                        "probabilities": {"0": 0.0, "1": 0.0, "2": 1.0} }
  },
  "usage": { "input_tokens": 469, "output_tokens": 77 }
}
```

Three properties of that shape are what the rest of this design rests on.

**The text is sent once and every question reads it.** The body is the expensive part of the request, so a fourth question about a body already on the wire costs a few tokens and no extra round trip. That is why this tool asks eight at a time rather than eight times.

```text
                        +-- has_repro          -> 0.84
   one issue body  -->  +-- report_kind        -> defect, confidence 1.00
   one round trip       +-- urgency_stated     -> 2.00 of 2
                        +-- ... five more

   469 input tokens for three questions.
   The eight the tool really asks cost about 2,400.
```

**Every answer is a number you can threshold.** `has_repro` came back 0.84, not "yes, the issue appears to contain reproduction steps". So the code compares it to a floor, and that floor lives in the code where a diff shows it. Nothing has to read a sentence to find out what the model decided.

**The model never does arithmetic.** It classifies, and the numbers it returns get combined in Go. Jev cannot count, and its comparisons of dates and quantities are unreliable, so every threshold, every sum and every sort in this tool happens outside it.

## What one issue costs

Two calls, and the split between them is the design.

```text
                  stage 1                          stage 2
  issue text  ->  8 questions about the text  ->   1 question about a
  title            kind, area, security,            fact table
  body             performance, blocking,
  labels           repro, solution, urgency         "when should somebody
                                                     pick this up?"
                           |
  GitHub API ------------->+                              |
  days open                                               v
  days idle                                      Today / This week /
  linked pull request                            This month / When time permits
  who filed it
  who has commented

  ~2,700 input tokens per issue. Both calls, 265 ms.
```

Stage one reads the issue. Stage two never sees the body at all, only a table of facts, so a persuasively written issue cannot argue past the evidence. An issue that says "CRITICAL, please fix immediately" and carries no repro, no proposed fix and no other commenters has to win on those facts.

## Why two calls instead of one

The split buys two things.

Stage one can be asked a question and checked. "Does this body contain a repro" has one right answer, visible to anyone who opens the issue. When the tool gets it wrong, you can see that it is wrong and reword the question.

Stage two works from a table small enough to read whole. When a bucket looks wrong, the input to that decision is a dozen lines, and you can look at them.

Here is all of it for one real issue, printed by `-explain 1575`:

```text
#1575  macOS: prepare-log-dirs.sh 0750/uid-1000 blocks the bind mount and
       nginx logging, so registry/auth-server/mcpgw never serve

stage 1, asked about the issue text
  kind                   defect
  security               0.05
  performance            0.01
  area                   deployment-and-ops
  blocking               0.01
  repro                  0.98
  solution               0.67
  urgency                1.82 of 2, "Cannot proceed without it"

stage 2 read exactly this, and no body
  kind: defect
  security: 0.05
  performance: 0.01
  area: deployment-and-ops
  blocking: 0.01
  repro: 0.98
  solution: 0.67
  urgency: 1.82 of 2, "Cannot proceed without it"
  filed by: an outside reporter
  days open: 67
  days since last activity: 65
  comments: 1 from 1 different person
  comments by the reporter: 1
  comments by a project maintainer: 0
  discussion: an outside reporter has commented and no maintainer has replied
  linked pull request: none

stage 2 answered work_horizon 2.85 of 3, which is "Today"
  2673 input tokens across both calls, 265 ms
```

Read the fact table and the answer follows. Somebody outside the project reported a defect that stops three services from starting, gave steps to reproduce it, said they cannot proceed, commented once, and has been waiting 65 days without a reply.

Compare the bottom of the queue, `-explain 805`:

```text
#805  Inconsistent tool extraction between hybrid and client-side search paths
  kind: defect
  filed by: a project maintainer
  days open: 179
  days since last activity: 179
  discussion: nobody has commented
  linked pull request: none
  labels: semantic-search, search, parking-lot

stage 2 answered work_horizon 0.42 of 3, which is "When time permits"
```

A real defect, filed by the maintainer, that nobody has commented on in six months and that carries a `parking-lot` label. Same questions, opposite answer, and the reasons are on the page.

## The areas are deliberately coarse

Six options: frontend, backend, infrastructure-as-code, deployment-and-ops, docs, unclear.

Each one is something a reporter can see from outside. A person filing an issue knows their problem showed up in the browser, or while deploying, or in the documentation. They do not know which internal package owns it, because that split exists in the codebase and not in their experience of the bug.

Measured over 284 closed issues whose fixes we could check, that line is sharp. Asking about an externally visible area agrees with where the fix landed 74% to 91% of the time. Asking about one internal service package against another drops to 10% to 16%, which is what guessing looks like.

So the question offers the six a body can decide, and `unclear` for the ones that say nothing about where the problem is.

## Security and performance sit outside the kind

`report_kind` offers one answer from defect, regression, feature-request, enhancement, question, docs-gap and debt. Those are mutually exclusive, so one question is right.

Security and performance are separate yes-or-no questions, because they overlay every kind. A security regression is both, and a single question forcing one answer would lose half of it. Issue #1834 came back `regression` with security at 0.95, which is the shape of a login failure behind a proxy.

## Who has been talking, counted rather than asked

Four facts come out of the comment thread, and GitHub knows all of them exactly:

```text
  7 comments on the issue
   |
   +-- 2 from the person who filed it
   +-- 1 from somebody with a project role
   +-- 2 from other people
   +-- 2 from bots, ignored
   |
   +-- 3 distinct humans
```

Each one says something a single number cannot. Three different people on a thread means more than one person is affected, which one insistent reporter cannot establish. A maintainer having replied means somebody already looked. An outside reporter posting four times with no reply means the issue is waiting on us, and 5 of the 106 are in exactly that state.

Who filed the issue decides whether that last one means anything. The maintainer filed 49 of these 106 issues, so a thread with no maintainer reply is often a maintainer talking on their own issue rather than somebody being ignored.

Bots are excluded two ways, because GitHub reports them two ways: a real bot account comes back with a `Bot` type, and an app posting through a user account comes back as a user whose login ends in `[bot]`. Without the second check, a CI job posting twelve build failures reads as a busy, contested thread.

## From a score to four buckets

The second question is a score with four levels, and the levels are the buckets:

```text
  work_horizon (score 0 to 3)

  3  Today                 ->  cap 5
  2  This week             ->  cap 15
  1  This month            ->  no cap
  0  When time permits     ->  no cap
```

A score rather than a pick-one, because Jev returns a fraction between levels, and that fraction orders the issues inside a bucket. #1575 came back at 2.85 and sits above an issue that came back at 2.61.

The levels are time horizons rather than a ranking for a reason worth naming. A ranking question like "would this make your top five" asks one issue to know what else is in the queue, and one call sees one issue. "Is this a today thing" is a judgement about the issue alone, and the selection of the actual five happens afterwards in Go, by sorting.

Rounding a score to its nearest level gives four buckets without any clustering. On the real queue the scores spread across all four, so natural breaks would add machinery for nothing. The tool prints the distribution on every run and says so if three quarters of a queue ever land in one bucket, which is when the cuts would be worth revisiting.

The two caps are capacity promises. A list of thirty things to do today is not a plan, so anything scoring into a full bucket moves down one and the row says it moved.

## What it produced

```text
| horizon           | issues | numbers                             |
| Today             | 4      | #1662, #1575, #1859, #1663          |
| This week         | 15     | #1571, #1607, #1597, #98, #895, ... |
| This month        | 67     | ...                                 |
| When time permits | 20     | ...                                 |
```

The four it says to start on, with the reasons column that makes the thing correctable:

| issue | kind | area | why it is here |
|---|---|---|---|
| #1662 | defect | infrastructure-as-code | urgent in the body (1.53 of 2), proposes a fix, outside reporter |
| #1575 | defect | deployment-and-ops | outside reporter commented once, no reply, urgent in the body (1.79 of 2), has a repro |
| #1859 | defect | backend | urgent in the body (1.58 of 2), has a repro, opened 0 days ago |
| #1663 | defect | deployment-and-ops | #1784 already open, says work is blocked (0.82), security (0.58) |

All four are defects. Three come with a way to reproduce them. One has a pull request already open against it, which the table says so you can go and finish it rather than start it.

### Checking it against work whose answer is known

`-state CLOSED -sample 25 -seed 7` scores a random sample of closed issues, which is a sanity check rather than a fitted target. The single issue it put in Today was #1834: a 1.31.0 regression where browser login fails behind an HTTP proxy, blocked, security 0.95, urgency 2.00 of 2. That was a real regression, and somebody did fix it.

One confound to know about: a closed issue has a large "days since last activity", which pushes it toward the bottom buckets. Ten of the twenty-five landed in "when time permits". The closed check is a fair read on the top of the ranking and an unfair one on the bottom.

## What it cannot do

The buckets are advice. The tool writes a markdown file and nothing else: it does not close, label, comment on or assign anything. TypeSafe publishes 67.8% accuracy on their own benchmark, their figure, September 2026, unreproduced outside the company. That is enough to decide what to read first and nowhere near enough to be the last word on anything.

Scores move between runs. The same issue asked twice comes back with a slightly different number, so one rerun moved the Today count from 4 to 5. Any score within 0.15 of a bucket edge is marked on its row, and a changed bucket is worth a rerun before it is worth a theory.

There is no accuracy figure for the buckets, because there is nothing honest to measure one against. The obvious candidate is how long an issue actually took to close, which the closed issues record exactly. Fitting to it would be a mistake: measured over 374 closed issues, the ones labelled `security` took thirty times longer to close than the ones labelled `bug`. That is a record of delay, and fitting weights to it would make the delay the goal.

So the judgement in this tool is declared rather than fitted. The weights are in `questions.yml` where you can read them, the thresholds are in `score.go` where a diff shows them, and the thing you check is the output.

## Running it

```bash
# Every open issue, into data/triage-owner-repo-open.md
issue-triage owner/repo

# A seeded sample of closed issues, as a sanity check
issue-triage owner/repo -state CLOSED -sample 25 -seed 7

# Everything behind one issue's bucket
issue-triage owner/repo -explain 1575
```

One static binary, no runtime, no per-repository configuration. It needs a GitHub token, from `GITHUB_TOKEN`, `GH_TOKEN` or the `gh` CLI, and a Jev key in `TYPESAFE_API_KEY` or a `.env` file.

A bucket you disagree with is a question worth rewording. The reasons column names the signal that put the issue there, `-explain` prints the whole decision, and the questions live in `questions.yml`.

## Sources

- The two runs behind every number here: `data/triage-agentic-community-mcp-gateway-registry-open.md` and `-closed.md`, both 7 October 2026.
- The worked examples: `issue-triage ... -explain 1575` and `-explain 805`, same day.
- The 284-issue measurement of the twelve-area version, which is why the areas are coarse now: recorded in the project handoff, 6 October 2026.
- Repository under test: [agentic-community/mcp-gateway-registry](https://github.com/agentic-community/mcp-gateway-registry).
- Jev price and accuracy figures are TypeSafe's own, September 2026.
