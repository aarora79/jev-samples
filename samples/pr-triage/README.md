# pr-triage

Say what evidence each open pull request needs before it merges, one Jev call each.

```text
  INPUT            DATA PREP           PRE-TRIAGE            JEV TRIAGE              OUTPUT
  a repo or        title, body,        draft, red CI         one call, eleven        one route: what
  a PR URL   --->  files, diff,  --->  or pending CI   --->  questions: nine   --->  would be enough
                   check runs          stops here            become effort           to merge this
                   from the API        at no cost            and consequence         change

  [18 open]  --->  [18 fetched]  --->  [10 stop here]  --->  [8 reach Jev]     --->  [6 clear on machines]
                                                                                     [2 need a person]

  [ ] = one real run, the 18 most recent open pull requests on apache/airflow
```

That bracketed row is the run worked through below. Rules remove ten before any model call, and Jev sorts the other eight by what evidence would settle each one: two clear on a green tick, three want a test, one wants an AI review, and two go to a person. The maintainer reads those two.

A maintainer with a queue of open pull requests wants to know which ones a glance clears and which ones need an hour. The sample sends Jev the title, the description, the file list and as much of the diff as fits, then asks eleven questions about each pull request in one call.

Nine of the answers become two numbers, because effort and consequence are different questions. **Effort** is their weighted mean: how long this takes to read. **Consequence** is the *max* of the four that say what breaks if it is wrong, never the mean, because a change that is safe in three ways and dangerous in one is a dangerous change.

Before any of that, a pull request has to be worth reading. Two calls per pull request, one to the checks API and one to the reviews API, settle four terminal states, and none of them spends a Jev call:

| State | Means | Waiting on |
| --- | --- | --- |
| `draft` | the author marked it draft | the author, who is not asking |
| `pending-author-rework` | a reviewer asked for changes against the commit the branch still points at | the author, who has not answered yet |
| `ci-failing` | a check concluded failure, timed out, or wants action | the checks, or the branch |
| `ci-pending` | a check is still running | nobody, come back later |

`ci-failing` states a fact and never judges the author. A check name that fails on several unrelated pull requests is the check being broken, and the output marks those failures as the checks' own.

`pending-author-rework` compares each reviewer's latest verdict against the pull request's head commit. A change request submitted against the current head means the author has pushed nothing since, so the next move is theirs and a second reviewer reading it now would be reading a diff its first reviewer has already rejected. Pushing a commit answers the request, and the state clears without anyone dismissing anything. It sits above the check states because a person has already read the diff and named the work, which says more than a red build.

The same comparison answers the opposite question, and the run prints that as its own table. A change request judged against an *older* commit than the head means the author has answered, so the pull request is back with the reviewer who asked:

```text
| Reviewer | Waiting | Pull requests       |
| -------- | ------- | ------------------- |
| omrishiv | 3       | #1678, #1764, #1783 |

Each of these asked for changes and the author has pushed since, so the pull request is back with that reviewer, oldest first.
1 of them cannot merge yet whatever the reviewer decides, being held up by a state above: #1764.
```

Those pull requests still take a route, because they are reviewable and the route still says what evidence a merge would need. The table answers who is holding them, which no route can. The second line matters on a busy queue: a reviewer sent to #1764 would find a red build waiting whatever they decide. The grouping lands in the JSON report as `awaiting_reviewer`, keyed by login, so a bot can go and ask.

On the eighteen airflow pull requests worked through below, three of those states fired and took ten out of the queue before Jev saw a single one. The fourth needs a repository where reviewers are active: over the 23 open pull requests on `agentic-community/mcp-gateway-registry`, one reviewer had asked for changes on two of them and the authors had pushed nothing since.

```text
PENDING-AUTHOR-REWORK  (2)  -> a reviewer asked for changes and the author has not pushed since: the next move belongs to the author, not to another reviewer
  #1748   changes requested by omrishiv, and no commits since
          fix(prm): resolve RFC 9728 path-aware per-server PRM under a path-prefixed registry_url
          https://github.com/agentic-community/mcp-gateway-registry/pull/1748
  #1713   changes requested by omrishiv, and no commits since
          fix(auth): normalize IdP group names before matching scope mappings
          https://github.com/agentic-community/mcp-gateway-registry/pull/1713
```

The pair picks a route, and the route answers one question: **what would be enough to merge this change?** Enough for a green tick to settle it, or enough that it wants a test, an AI review, or a person.

The tool merges nothing and tells nobody to merge now. TypeSafe reports 67.8% accuracy on their own benchmark, published September 2026, which is enough to decide how much evidence to demand and nowhere near enough to be the last gate before main.

| Route | Enough to merge on |
| --- | --- |
| `green-is-enough` | CI passing |
| `tests-are-enough` | CI passing, and a test that exercises the change |
| `ai-review-is-enough` | the above, and an AI review that finds nothing |
| `human-required` | a person reads it, whatever the machines say |
| `human-plus-author` | a person reads it line by line, with the author walking them through |

Consequence sets the floor and effort can only raise it. Of those eighteen, 8 were reviewable and cost about two tenths of a cent to route.

## What each stage costs

Of the eighteen pull requests below, a frontier review is the deciding evidence on one. Ten never reach a model, removed by rules about drafts and check runs. Jev reads the remaining eight, at 3,040 input tokens and 171 ms each.

Pre-triage is `if` statements over the checks API. No model can do that stage, because no model knows whether CI passed.

Routing the eight is a closed question: eleven judgements, two numbers, one route. A frontier model can answer it, reading the same diff and returning the same eleven answers at its own price. Routing one pull request costs about 3,000 input tokens whoever does it, which is $0.00013 at TypeSafe's $0.042 per million, published September 2026. Divide your model's input price by 0.042 for the multiple.

Reading code for defects needs a model that can read code, which on this queue is one pull request in eighteen. Routing one takes 164 ms, so 710 of them took two minutes.

The saving holds while the cheap routes are right. A pull request sent to `green-is-enough` that needed reading was false economy whatever it cost, so the number to measure is precision on that route.

## What it asks

Eleven questions go to Jev in one call: how far the change reaches, whether it touches auth or secrets, how many judgment calls a reviewer has to agree with, and eight more. Nine carry a weight in [questions.yml](questions.yml). Four of those nine also feed the consequence axis, marked below.

![The pr-triage flow: a pull request goes through pre-triage, which is plain rules and no model. Any of draft, a failing check or a running check stops there and reports that state. Otherwise one Jev call asks eleven questions, nine weighted and two labels, the nine become an effort and consequence pair, and the pair picks one route saying what the repo owner should do. Two worked examples end the diagram.](assets/flow.png)

[assets/flow.html](assets/flow.html) is the source. Edit the HTML and run `python3 assets/render.py` to rebuild the PNG.

| Question | Type | Weight | What a high answer means |
| --- | --- | --- | --- |
| `change_kind` | Choice | | docs, tests, dependency, config, bugfix, feature or refactor |
| `review_focus` | Choice | | where a reviewer should start |
| `blast_radius` **(c)** | Score | 0.15 | the change reaches shared code paths |
| `design_decisions` | Score | 0.14 | a reviewer has judgment calls to agree with |
| `security_surface` **(c)** | Noul | 0.15 | it touches auth, secrets, tokens or input validation |
| `mechanical` | Noul (inverted) | 0.12 | one edit repeated, so checking one instance checks them all |
| `breaking_change` **(c)** | Noul | 0.11 | an existing caller stops working |
| `scope_creep` | Noul | 0.09 | the diff does things the description never mentions |
| `infra_surface` **(c)** | Noul | 0.08 | it touches deployment, images or CI |
| `has_tests` | Noul (inverted) | 0.08 | the change comes with tests that exercise it |
| `description_quality` | Score (inverted) | 0.08 | the author explained what, why and how they checked |

`questions.yml` inverts three of them, so a mechanical diff, a covered change and a thorough description each lower the effort. The four marked **(c)** also feed consequence, which takes the *max* of them rather than the mean: `security_surface` at 0.98 survives an `infra_surface` of 0.04, where a mean would drag it to the middle. A score feeding consequence reads the probability on its *top* level rather than its normalised score, because `blast_radius` level 1 is "confined to one module", which is ordinary work rather than half a catastrophe.

## Running it

```bash
cd samples/pr-triage

# The 10 most recent open pull requests, fetched and triaged in one command
uv run pr_triage.py agentic-community/mcp-gateway-registry

# Every open one, or everything opened in the last 30 days
uv run pr_triage.py owner/repo --all
uv run pr_triage.py owner/repo --since 30
uv run pr_triage.py owner/repo --since 2026-08-01

# Build a dataset once, then triage it as often as you like with no GitHub calls
uv run fetch_prs.py owner/repo --all
uv run pr_triage.py --dataset data/owner-repo-open-all.json

# Every question for one pull request, and what each one contributed
uv run pr_triage.py --dataset data/owner-repo-open-all.json --explain 1693
```

`fetch_prs.py` needs a GitHub token and no Jev key. It reads `GITHUB_TOKEN` or `GH_TOKEN`, and falls back to `gh auth token`. Unauthenticated callers get sixty requests an hour, and one pull request costs three of them: the listing, its files, and its check runs. Any real queue wants a token.

`pr_triage.py` needs a Jev key in `TYPESAFE_API_KEY`, from the environment or from `.env` beside the sample or at the repo root.

### The same check without Python

[`go/`](go/) holds a Go port that compiles both halves into one static binary, which is what the skill installs and what a CI runner wants. It works against github.com and a GitHub Enterprise Server host.

The Python stays canonical. `go/questions.yml` is a copy, `build.sh` refreshes it before every build, and `payload_test.go` fails when the two drift, so the two tools cannot disagree about one pull request in silence.

## Install it as a Claude Code skill

One command puts the tool and the skill on the machine. After that the skill does the work, and nobody has to learn the flags.

```bash
curl -fsSL https://raw.githubusercontent.com/aarora79/jev-samples/main/samples/pr-triage/vend/install.sh | sh
```

That installs two things:

| What | Where | Why |
| --- | --- | --- |
| the `pr-triage` binary | `/usr/local/bin`, or `~/.local/bin` | fetches the pull requests and triages them, no Python needed |
| `SKILL.md` | `~/.claude/skills/pr-triage/` | tells the agent when to triage and how to read the result |

Set `BINDIR` or `SKILL_DIR` to put either somewhere else. The installer checks both credentials and names the one you are missing rather than failing halfway through a run:

```bash
export GITHUB_TOKEN=...        # or GH_TOKEN, GH_ENTERPRISE_TOKEN, or run gh auth login
export TYPESAFE_API_KEY=...
```

Then start a new Claude Code session and ask for a triage in your own words:

> triage the open pull requests on apache/airflow
>
> which of our open PRs need a real review this week?
>
> triage https://ghe.example.com/platform/gateway and tell me what to read first

The skill picks the flags, runs the binary, reads the report and tells you which pull requests need an hour and which clear on a glance. It writes a JSON report and a markdown one you can paste into an issue.

For CI rather than a conversation, [`go/README.md`](go/README.md) covers the binary on its own, including `-fail-on-route` for failing a job and the three ways to point it at a GitHub Enterprise Server host.

## What it prints

From a run against the eighteen most recent open pull requests of [apache/airflow](https://github.com/apache/airflow) on 26 September 2026. The summary comes first, and it accounts for every pull request fetched:

```text
| Outcome             | Count | Pull requests                                                  |
| ------------------- | ----- | -------------------------------------------------------------- |
| draft               | 1     | #73720                                                         |
| ci-failing          | 8     | #73743, #73737, #73734, #73732, #73730, #73726, #73724, #73723 |
| ci-pending          | 1     | #73740                                                         |
| green-is-enough     | 2     | #73742, #73722                                                 |
| tests-are-enough    | 3     | #73719, #73727, #73729                                         |
| ai-review-is-enough | 1     | #73728                                                         |
| human-required      | 2     | #73736, #73741                                                 |
```

The first three rows are the pre-triage states, settled by rules before any model call. Ten of the eighteen stopped there. The re-review table comes next, and this run predates it: the committed report in [data/](data/) holds the two tables below and not that one. On a queue where nobody is owed a second look it prints one line saying so.

The eight that reached Jev, one row each:

```text
| PR     | Title                                        | Files | Lines    | Kind       | Effort | Cons | Cons from        | Route                       |
| ------ | -------------------------------------------- | ----- | -------- | ---------- | ------ | ---- | ---------------- | --------------------------- |
| #73728 | Discover a bundle's Dag definitions throu... | 3     | +203/-34 | feature    | 0.49   | 0.53 | breaking change  | ai-review-is-enough         |
| #73741 | Improve DAG tag length validation error      | 2     | +11/-3   | bugfix     | 0.40   | 0.97 | security surface | human-required              |
| #73719 | Fix masked failure reason for deferrable ... | 4     | +103/-8  | bugfix     | 0.37   | 0.33 | breaking change  | tests-are-enough (on a cut) |
| #73727 | Speed up zip Dag discovery and keep membe... | 2     | +53/-23  | bugfix     | 0.35   | 0.23 | breaking change  | tests-are-enough            |
| #73736 | Bump astral-sh/setup-uv from 10.1.0 to 10... | 4     | +4/-4    | dependency | 0.28   | 0.99 | infra surface    | human-required              |
| #73742 | Account for the open pull request limit i... | 3     | +36/-0   | docs       | 0.28   | 0.07 | infra surface    | green-is-enough             |
| #73729 | Fix clearing with upstream and downstream... | 2     | +51/-2   | bugfix     | 0.27   | 0.17 | breaking change  | tests-are-enough (on a cut) |
| #73722 | Clarify max_db_retries doc wording to avo... | 1     | +2/-1    | docs       | 0.19   | 0.04 | breaking change  | green-is-enough             |
```

`Effort` is the weighted mean of nine questions and `Cons` is the max of four. `Cons from` names the question that produced the consequence, which is what set the route: #73741 is a two-file bugfix that touches validation, so security surface at 0.97 carries it past four heavier changes. An effort marked `(size)` means the file and line counts raised it. Either number marked `(on a cut)` sits within 0.02 of a threshold, which is about how far repeat calls move it, so read it as either side.

Then the ten that stopped at pre-triage, with what each is waiting on:

```text
### Not reviewable yet: 10 of 18

DRAFT  (1)  -> the author is still working: nothing to review, and nothing to decide
  #73720  marked draft by the author
          [v3-3-test] Raise DeadlockImminentError for sync comms calls from a paused event loop thread (#73521)
          https://github.com/apache/airflow/pull/73720

CI-FAILING  (8)  -> the branch cannot merge until the checks pass, so review waits on that
          5 of these fail only on a check that fails elsewhere too, so they are waiting on the checks rather than on their authors
  #73743  1 of 95 checks failing: Postgres tests: core / DB-core:Postgres:14:3.10:Core...Serialization, which also fails on other pull requests
          Fix airflowctl dags get-tags crashing whenever a Dag has tags
          https://github.com/apache/airflow/pull/73743
  #73737  1 of 100 checks failing: Basic tests / Scripts tests, which also fails on other pull requests
          Bump the github-actions-updates group with 4 updates
          https://github.com/apache/airflow/pull/73737
  #73734  1 of 75 checks failing: Postgres tests: core / DB-core:Postgres:14:3.10:Core...Serialization, which also fails on other pull requests
          Bump the edge-ui-package-updates group across 1 directory with 8 updates
          https://github.com/apache/airflow/pull/73734
  #73732  1 of 100 checks failing: Basic tests / Scripts tests, which also fails on other pull requests
          Bump boto3 from 1.43.96 to 1.43.98 in /dev/breeze in the 3-3-uv-dependency-updates group
          https://github.com/apache/airflow/pull/73732
  #73730  1 of 100 checks failing: Postgres tests: core / DB-core:Postgres:14:3.10:Core...Serialization, which also fails on other pull requests
          Bump boto3 from 1.43.97 to 1.43.98 in /dev/breeze in the uv-dependency-updates group
          https://github.com/apache/airflow/pull/73730
  #73726  2 of 100 checks failing: 2 checks
          UI: Add team filter to Human-in-the-loop task instances listing
          https://github.com/apache/airflow/pull/73726
  #73724  1 of 100 checks failing: Additional PROD image tests / Test e2e integration tests with PROD image / Regular e2e test
          Fix Grid and Graph 500 errors for cyclic TaskGroup dependencies
          https://github.com/apache/airflow/pull/73724
  #73723  1 of 88 checks failing: Additional PROD image tests / TypeScript SDK e2e tests with PROD image / TypeScript SDK e2e test
          TS SDK: embed one source region per native Dag file
          https://github.com/apache/airflow/pull/73723

CI-PENDING  (1)  -> checks are still running: come back when they land
  #73740  1 of 89 checks still running
          Bump the auth-ui-package-updates group across 1 directory with 8 updates
          https://github.com/apache/airflow/pull/73740

56% of this queue cannot be reviewed as it stands. Fix that before reading anything into the routes.
```

Then the same pull requests grouped by route, costliest evidence first, with the reasoning and the one signal that would drop each a route:

```text
HUMAN-REQUIRED  (2)  -> a person reads this before it merges, whatever the machines say
  #73736  consequence 0.99 (infra surface), effort 0.28  Bump astral-sh/setup-uv from 10.1.0 to 10.2.0 in the github-actions-updates group
          drivers: infra surface, has tests, blast radius
          would drop a route with: evidence that infra surface is covered, a test or a reviewer who owns it
          https://github.com/apache/airflow/pull/73736
  #73741  consequence 0.97 (security surface), effort 0.40  Improve DAG tag length validation error
          drivers: security surface, blast radius, mechanical
          would drop a route with: evidence that security surface is covered, a test or a reviewer who owns it
          https://github.com/apache/airflow/pull/73741

AI-REVIEW-IS-ENOUGH  (1)  -> an AI review that finds nothing is sufficient, plus green CI and tests
  #73728  consequence 0.53 (breaking change), effort 0.49  Discover a bundle's Dag definitions through the importer registry
          drivers: design decisions, blast radius, mechanical
          would drop a route with: evidence that breaking change is covered, a test or a reviewer who owns it
          https://github.com/apache/airflow/pull/73728

GREEN-IS-ENOUGH  (2)  -> merge when CI is green: nothing here needs a person
  #73742  consequence 0.07 (infra surface), effort 0.28  Account for the open pull request limit in the PR triage process
          drivers: mechanical, has tests, design decisions
          https://github.com/apache/airflow/pull/73742
  #73722  consequence 0.04 (breaking change), effort 0.19  Clarify max_db_retries doc wording to avoid off-by-one confusion
          drivers: has tests
          https://github.com/apache/airflow/pull/73722
```

That listing leaves out the `tests-are-enough` section for length, and the committed report in [data/](data/) has all four. Two of the eighteen need a person, two more need nothing but a green tick, and the eight red branches above are what this queue waits on.

Then what the run cost:

```text
8 pull requests, 11 questions each, one call apiece. 24,317 input tokens, 1,367 ms total, 171 ms per call on average, $0.00102 at $0.042 per million input tokens.
```

Every run writes two reports into [data/](data/). The JSON one holds each answer as Jev sent it, next to the credit, the consequence, the route and the tier the sample derived from it, plus `route_counts` and `awaiting_reviewer` roll-ups so a job can read the shape of a queue, and who is holding it, without walking every entry. The markdown one carries the same three tables and the same route sections, with the numbers as links, so a triage pastes into a pull request or an issue without reformatting. This repo commits the `apache-airflow` pair as the worked example and ignores the rest, because triaging somebody's open pull requests is their business.

### One pull request, from eleven answers to one route

Airflow #73741 improves the error a DAG tag length check raises. Two files, +11/-3, and Jev read all of the diff. The nine weighted answers, heaviest contribution first, with the four that also feed consequence marked **(c)**:

| Question | Jev returned | Weight | Credit | Adds to effort | Feeds consequence |
| --- | --- | --- | --- | --- | --- |
| `security_surface` **(c)** | 0.97 | 0.15 | 0.97 | 0.1455 | **0.97** |
| `blast_radius` **(c)** | 1.21 / 2 | 0.15 | 0.60 | 0.0907 | 0.22 |
| `mechanical` (inverted) | 0.39 | 0.12 | 0.61 | 0.0732 | |
| `design_decisions` | 0.98 / 2 | 0.14 | 0.49 | 0.0686 | |
| `breaking_change` **(c)** | 0.07 | 0.11 | 0.07 | 0.0077 | 0.07 |
| `scope_creep` | 0.08 | 0.09 | 0.08 | 0.0072 | |
| `has_tests` (inverted) | 0.97 | 0.08 | 0.03 | 0.0024 | |
| `infra_surface` **(c)** | 0.02 | 0.08 | 0.02 | 0.0016 | 0.02 |
| `description_quality` (inverted) | 2.00 / 2 | 0.08 | 0.00 | 0.0000 | |
| | | **1.00** | | **0.3969** | **max 0.97** |

Effort is that column added up: 0.3969, a short read of two files. The report records 0.3970, because it rounds the total rather than adding up nine rounded rows. Consequence is the max of the four marked entries: 0.97, set by `security_surface`. Effort clears no raise, consequence clears its top cut, so the route is `human-required` and the consequence axis decided it alone.

The title reads like an error-message tweak, and the diff is eleven added lines. It still needs a person, because a length check on user input is input validation, and `security_surface` came back 0.97. That is the case a single blended score buries: averaging the nine puts this at 0.40, in the middle of the queue, where nobody would look twice.

Three rows show the axes reading one answer two ways. `blast_radius` scored 1.21 out of 2, so it contributes 0.60 of credit to effort, a fair chunk of a short read. Its probability on the top level, "reaches shared code paths that many callers depend on", is 0.22, so it contributes 0.22 to consequence and stops there. `has_tests` came back 0.97, and inverted that adds 0.0024 to effort, because a tested change is quicker to read. It feeds consequence not at all, since tests are evidence against regression and not against a validation mistake. `description_quality` at 2.00 out of 2 contributes 0.0000, which is what a thorough description earns.

The run also prints the one thing that would move it: a test exercising that security surface, or a reviewer who owns the path.

## What to notice

**Ten of eighteen were waiting on a build or their author.** One draft, eight with a failing check, one still running, and none of them reached Jev. The eight that were reviewable came out 2 `green-is-enough`, 3 `tests-are-enough`, 1 `ai-review-is-enough`, 2 `human-required`. A queue hides two cheap groups: the changes nobody needs to read, and the ones stuck behind a build.

**One set of cuts produces two different distributions.** The same cuts over 710 airflow pull requests opened in the last 100 days took 342 of them, 48%, off the human queue. Run against a repository where every change touches authentication or deployment credentials, they took none of 24, because that queue contains no low-consequence work to find. Airflow's cheap tail is its 48 docs pull requests, 20 dependency bumps and 22 test-only changes. A repository without those has no cheap tail, and a router that invented one would be wrong.

**Nine files can outrank fifty-four.** #73704 changes 89 lines across 9 files and routes `human-required` at consequence 0.84, because it fixes socket leaks and adds request timeouts across providers and `security_surface` came back 0.84. #73713 changes 598 lines across 54 files and its effort is only 0.37, because `mechanical` came back 0.81 on one locale-formatting edit repeated through the UI. The eleven questions sort by what a review has to catch, and the driver line names the one that did it.

**Arithmetic stays in code.** Jev cannot count, so the file and line totals reach it as a sentence for context, and every threshold on a number lives in [pr_triage.py](pr_triage.py) and its Go twin. `SIZE_FLOORS` names the lowest effort tier a change of a given size can land in: over 30 files or 1,500 lines is high whatever Jev returned. `CONSEQUENCE_FLOORS` and `EFFORT_RAISES` turn the two axes into a route. Every one of them only ever raises, and the output marks the rows where it did, so a reader sees the disagreement instead of inheriting it.

**Say how much of the diff the model read.** All eight of the airflow changes fit inside the 24,000-character budget, but most pull requests on a feature-heavy repository do not: over the 26 open on mcp-gateway-registry, 22 hold more patch than that, so Jev read some files whole and the rest as paths and line counts. `_diff_text` fills the budget with the smallest patches first, because triage asks how far a change reaches and whether one edit repeats, and both of those want breadth. Coverage lands in the report as `diff_coverage` and in the grouped output as a line under any pull request below 70%. The 0.74 effort on #1693 came from 37% of a 51-file diff, which is a weaker claim than the same number across all of it, and the size floor guards those rows.

**Weights are a data change, thresholds are a code change.** Raising `security_surface` to 0.25 means editing one line of `questions.yml`. Moving a route cut means editing `CONSEQUENCE_FLOORS`. Both sit in a diff, and neither hides inside a question's wording.

**The description is evidence, and the author wrote it.** An author who opens with "trivial, please merge" is arguing for their own triage. The state names that field `description_written_by_the_author` and `scope_creep` asks whether the diff matches it, which turns the claim into something to check.

**Answers move between runs, so both axes carry a deadband.** Two runs of one dataset moved a load by up to 0.03 and a consequence by up to 0.07. Either number within 0.02 of its cut prints `(on a cut)` rather than picking a side. Running the Python and the Go binary over the same eighteen picked the same ten to stop at pre-triage, marked the same shared failures, and agreed on 7 of the 8 routes. They split on #73729, which came back at consequence 0.17 from Python and 0.10 from Go, either side of the 0.15 cut, and the Python run printed it as `(on a cut)`.

## Calibrate before you trust a route

TypeSafe reports 67.8% accuracy on their own benchmark, published September 2026. A router survives that, because a routing mistake costs little: send something to a human who did not need to look and you waste twenty minutes, while CI, the tests and an AI review all still stand behind a cheaper route. A merge decision cannot live with it, because nothing stands behind that.

The number to measure is **precision on `green-is-enough`**, where a false "this is safe" is the only expensive mistake the tool can make. Replay fifty pull requests your team already merged, count how many the sample would have sent to `green-is-enough`, and check how many of those were later reverted or hot-fixed. Move `CONSEQUENCE_FLOORS` until that count is zero. [jevcal](https://github.com/abhixhek/jevcal) turns that comparison into a measurement.
