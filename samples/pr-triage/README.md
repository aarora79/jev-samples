# pr-triage

Say what evidence each open pull request needs before it merges, one Jev call each.

A maintainer with a queue of open pull requests wants to know which ones a glance clears and which ones need an hour. The sample sends Jev the title, the description, the file list and as much of the diff as fits, then asks eleven questions about each pull request in one call.

Nine of the answers become two numbers, because effort and consequence are different questions. **Effort** is their weighted mean: how long this takes to read. **Consequence** is the *max* of the four that say what breaks if it is wrong, never the mean, because a change that is safe in three ways and dangerous in one is a dangerous change.

Before any of that, a pull request has to be worth reading. One call to the checks API per pull request settles three terminal states, and none of them spends a Jev call:

| State | Means | Waiting on |
| --- | --- | --- |
| `ci-failing` | a check concluded failure, timed out, or wants action | the checks, or the branch |
| `ci-pending` | a check is still running | nobody, come back later |
| `draft` | the author marked it draft | the author, who is not asking |

`ci-failing` states a fact and never judges the author. A check name that fails on several unrelated pull requests is the check being broken, and the output marks those failures as the checks' own.

Those three states took ten of eighteen out of the queue before Jev saw a single one.

The pair picks a route, and the route answers one question: **what would be enough to merge this change?** Enough for a green tick to settle it, or enough that it wants a test, an AI review, or a person.

None of them says merge it now, and none of them merges anything. TypeSafe reports 67.8% accuracy on their own benchmark, published September 2026, which is enough to decide how much evidence to demand and nowhere near enough to be the last gate before main.

| Route | Enough to merge on |
| --- | --- |
| `green-is-enough` | CI passing |
| `tests-are-enough` | CI passing, and a test that exercises the change |
| `ai-review-is-enough` | the above, and an AI review that finds nothing |
| `human-required` | a person reads it, whatever the machines say |
| `human-plus-author` | a person reads it line by line, with the author walking them through |

Consequence sets the floor and effort can only raise it. Of those eighteen, 8 were reviewable and cost about two tenths of a cent to route.

## Why a small model routes and a large one reviews

Of the eighteen pull requests below, a frontier review is the deciding evidence on **one**. Ten never reach a model at all, removed by plain rules about drafts and check runs. Jev reads the remaining eight, at 3,039 input tokens and 164 ms each.

Three things produce that funnel.

**The cheapest call is the one nobody makes.** Pre-triage is `if` statements over the checks API. It took ten of eighteen out of the queue before any model ran, and no model could have done it, because no model knows whether CI passed.

**Deciding what a human sees is itself a model call, and it should be the cheap one.** That decision is a closed question with a small answer: eleven judgements, two numbers, one route. A frontier model can make it. It would read the same diff, return the same eleven answers, and charge frontier prices for the privilege. Routing one pull request costs about 3,000 input tokens whoever does it, which is $0.00013 at TypeSafe's $0.042 per million, published September 2026. Divide your own model's input price by 0.042 and that is the multiple you pay to have the expensive model make the same decision.

**The expensive model then does the job only it can do.** Reading code for defects needs a model that can read code. On this queue a review is the deciding evidence on one pull request in eighteen, so that is how often it runs. Latency follows the same shape: 164 ms a pull request to route, so 710 of them took two minutes.

One condition on all of it. The saving is real only while the cheap routes are right. A pull request sent to `green-is-enough` that needed reading is false economy, however little it cost, which is why the number to measure is precision on that route rather than accuracy across the queue.

## What it asks

Eleven questions go to Jev in one call: how far the change reaches, whether it touches auth or secrets, how many judgment calls a reviewer has to agree with, and eight more. Nine carry a weight in [questions.yml](questions.yml). Four of those nine also feed the consequence axis, marked below.

![The pr-triage flow: a pull request goes through pre-triage, which is plain rules and no model. Any of draft, a failing check or a running check stops there and reports that state. Otherwise one Jev call asks eleven questions, nine weighted and two labels, the nine become an effort and consequence pair, and the pair picks one route saying what the repo owner should do. Two worked examples end the diagram.](assets/flow.png)

That diagram is built from [assets/flow.html](assets/flow.html). Edit the HTML and run `python3 assets/render.py` to rebuild the PNG.

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

`questions.yml` inverts three of them, so a mechanical diff, a covered change and a thorough description each lower the effort. The four marked **(c)** also feed consequence, where the *max* of them is taken rather than the mean: `security_surface` at 0.98 is not offset by `infra_surface` at 0.04. A score feeding consequence reads the probability on its *top* level rather than its normalised score, because `blast_radius` level 1 is "confined to one module", which is ordinary work rather than half a catastrophe.

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

From a run against the eighteen most recent open pull requests of [apache/airflow](https://github.com/apache/airflow) on 26 September 2026. Ten never reached Jev:

```text
## Not reviewable yet: 10 of 18

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

56% of this queue cannot be reviewed as it stands. Fix that before reading anything into the routes below.
```

The eight that were reviewable:

```text
| PR     | Files | Lines    | Kind       | Load | Cons | Route                       | Title                                                      |
| ------ | ----- | -------- | ---------- | ---- | ---- | --------------------------- | ---------------------------------------------------------- |
| #73728 | 3     | +203/-34 | feature    | 0.49 | 0.54 | ai-review-is-enough         | Discover a bundle's Dag definitions through the importe... |
| #73741 | 2     | +11/-3   | bugfix     | 0.40 | 0.97 | human-required              | Improve DAG tag length validation error                    |
| #73719 | 4     | +103/-8  | bugfix     | 0.35 | 0.28 | tests-are-enough            | Fix masked failure reason for deferrable EMR Serverless... |
| #73727 | 2     | +53/-23  | bugfix     | 0.35 | 0.22 | tests-are-enough            | Speed up zip Dag discovery and keep member file names      |
| #73736 | 4     | +4/-4    | dependency | 0.28 | 0.99 | human-required              | Bump astral-sh/setup-uv from 10.1.0 to 10.2.0 in the gi... |
| #73742 | 3     | +36/-0   | docs       | 0.27 | 0.07 | green-is-enough             | Account for the open pull request limit in the PR triag... |
| #73729 | 2     | +51/-2   | bugfix     | 0.27 | 0.15 | tests-are-enough (on a cut) | Fix clearing with upstream and downstream selecting unr... |
| #73722 | 1     | +2/-1    | docs       | 0.20 | 0.04 | green-is-enough             | Clarify max_db_retries doc wording to avoid off-by-one ... |
```

`Load` is effort and `Cons` is consequence. A load marked `(size)` means the file and line counts raised it. Either number marked `(on a cut)` sits within 0.02 of a threshold, which is about how far repeat calls move it, so read it as either side.

Then the same pull requests grouped by route, cheapest evidence last:

```text
GREEN-IS-ENOUGH  (4)  -> merge when CI is green: nothing here needs a person
  #73725  consequence 0.14 (blast radius), effort 0.18  Fix Elasticsearch and OpenSearch response wrapper bugs
          drivers: blast radius
          https://github.com/apache/airflow/pull/73725
  #73709  consequence 0.10 (blast radius), effort 0.29  Avoid repeated KubernetesExecutor pod deletion for duplicate events
          drivers: design decisions, blast radius, mechanical
          https://github.com/apache/airflow/pull/73709
  #73711  consequence 0.09 (breaking change), effort 0.27  Emit queued_duration metric when a task enters RUNNING via the execution API
          drivers: design decisions, blast radius, mechanical
          https://github.com/apache/airflow/pull/73711
  #73722  consequence 0.04 (breaking change), effort 0.19  Clarify max_db_retries doc wording to avoid off-by-one confusion
          drivers: has tests
          https://github.com/apache/airflow/pull/73722

HUMAN-PLUS-AUTHOR  (1)  -> a person reads it line by line, and the author walks them through it
  #73698  consequence 0.99 (security surface), effort 0.63  Bind the AWS auth manager SAML response to the browser that started the login
          drivers: security surface, design decisions, mechanical
          would drop a route with: evidence that security surface is covered, a test or a reviewer who owns it
          https://github.com/apache/airflow/pull/73698
```

Four of the eighteen need nothing but a green tick. One needs a person and the author in the room, and the line under it names the single thing that would drop it a route.

```text
18 pull requests, 11 questions each, one call apiece. 87,483 input tokens, 2,907 ms total, 162 ms per call on average, $0.00367 at $0.042 per million input tokens.
2 of 18 had patches too large to send whole, so Jev read part of the diff and the paths of the rest. Those are the ones the size floor guards.
```

Every run writes two reports into [data/](data/). The JSON one holds each answer as Jev sent it, next to the credit, the consequence, the route and the tier the sample derived from it, plus a `route_counts` roll-up so a job can read the shape of a queue without walking every entry. The markdown one carries the same table and route sections, so a triage pastes into a pull request or an issue without reformatting. This repo commits the `apache-airflow` pair as the worked example and ignores the rest, because triaging somebody's open pull requests is their business.

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

**Nine files can outrank fifty-four.** #73704 changes 89 lines across 9 files and routes `human-required` at consequence 0.84, because it fixes socket leaks and adds request timeouts across providers and `security_surface` came back 0.84. #73713 changes 598 lines across 54 files and its effort is only 0.37, because `mechanical` came back 0.81 on one locale-formatting edit repeated through the UI. Size is not review cost, and neither is line count.

**Arithmetic stays in code.** Jev cannot count, so the file and line totals reach it as a sentence for context, and every threshold on a number lives in [pr_triage.py](pr_triage.py) and its Go twin. `SIZE_FLOORS` names the lowest effort tier a change of a given size can land in: over 30 files or 1,500 lines is high whatever Jev returned. `CONSEQUENCE_FLOORS` and `EFFORT_RAISES` turn the two axes into a route. Every one of them only ever raises, and the output marks the rows where it did, so a reader sees the disagreement instead of inheriting it.

**Say how much of the diff the model read.** Five of those eighteen hold more patch than the 24,000-character budget, so Jev read some files whole and the rest as paths and line counts. `_diff_text` fills the budget with the smallest patches first, because triage asks how far a change reaches and whether one edit repeats, and both of those want breadth. Coverage lands in the report as `diff_coverage` and in the grouped output as a line under any pull request below 70%. The 0.37 on #73713 came from 44% of its diff, which is a weaker claim than the same number across all of it, and the size floor guards those rows.

**Weights are a data change, thresholds are a code change.** Raising `security_surface` to 0.25 means editing one line of `questions.yml`. Moving a route cut means editing `CONSEQUENCE_FLOORS`. Both sit in a diff, and neither hides inside a question's wording.

**The description is evidence, and the author wrote it.** An author who opens with "trivial, please merge" is arguing for their own triage. The state names that field `description_written_by_the_author` and `scope_creep` asks whether the diff matches it, which turns the claim into something to check.

**Answers move between runs, so both axes carry a deadband.** Two runs of one dataset moved a load by up to 0.03 and a consequence by up to 0.07. Either number within 0.02 of its cut prints `(on a cut)` rather than picking a side. Running the Python and the Go binary over the same eighteen agreed on 16 routes, and both disagreements were pull requests sitting on a cut, which both outputs said so.

## Calibrate before you trust a route

TypeSafe reports 67.8% accuracy on their own benchmark, published September 2026. A router survives that, because a routing mistake costs little: send something to a human who did not need to look and you waste twenty minutes, while CI, the tests and an AI review all still stand behind a cheaper route. A merge decision cannot live with it, because nothing stands behind that.

So the number to measure is not accuracy across the queue. It is **precision on `green-is-enough`**, where a false "this is safe" is the only expensive mistake the tool can make. Replay fifty pull requests your team already merged, count how many the sample would have sent to `green-is-enough`, and check how many of those were later reverted or hot-fixed. Move `CONSEQUENCE_FLOORS` until that count is zero. [jevcal](https://github.com/abhixhek/jevcal) turns that comparison into a measurement.
