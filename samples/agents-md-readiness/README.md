# agents-md-readiness

A coding agent is only as good as the instructions it finds in your repo. [AGENTS.md](https://agents.md) is where those instructions go, and its homepage claimed "over 60k open-source projects" when I read it on 19 September 2026, counted by [this GitHub code search](https://github.com/search?q=path%3AAGENTS.md+NOT+is%3Afork+NOT+is%3Aarchived&type=code). The file decides whether an agent runs your real test command or guesses, edits the generated file you told it never to touch, or opens a pull request in the wrong format.

Legacy repos have no AGENTS.md at all. Repos that have one wrote it against a format they skimmed, so it misses the sections the spec recommends. Worst of all it drifts: the build command changes, the test runner moves, a directory gets renamed, and the file keeps telling every agent the old story. Nothing in a normal review catches that, because nobody reads AGENTS.md in a diff.

This sample turns the file into something CI can check. Wire it into the job that runs when a pull request opens, score the AGENTS.md or CLAUDE.md the branch would merge, and fail the build when readiness drops below the bar you set or when the file leaks a credential. Each check is one call carrying 17 questions, and on the eleven repos measured here it cost between $0.00004 and $0.00034.

## What eleven public repos cost to score

On 19 September 2026 this sample scored the AGENTS.md or CLAUDE.md at the root of eleven public repos. Ten had a file, and the eleventh has neither. Every call asked all 17 questions at once.

| Measured across the ten scored repos | |
| --- | --- |
| Input tokens, all ten calls | 38,945 |
| Cost, all ten calls | $0.0016 |
| Cost per repo | $0.00004 to $0.00034 |
| Latency per repo, end to end | 317 to 476 ms, mean 381 ms |
| Output tokens | free, and counted |

Pricing is TypeSafe's published $0.042 per million input tokens, September 2026, which `questions.yml` carries as `input_usd_per_million`. A repo merging 100 pull requests a month, checking its AGENTS.md on every one, pays about three cents a month and waits under half a second per check.

`Missing of 16` counts how many of the sixteen weighted checks the file said nothing about, scoring under 0.15 out of 1.00: 13 means a file that answers almost nothing, 1 means a single gap. `Weakest area` is the one thing Jev would fix first, out of five: **commands** (setup, build and run), **testing** (how to run the tests and what to do when one fails), **conventions** (code style and naming rules), **layout** (where things live in the repo), **boundaries** (what an agent must never do, and what needs a human first).

| Repo | File | Readiness | Weakest area | Missing of 16 | Tokens | Latency | Report |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `vercel/next.js` | AGENTS.md | 0.95 | boundaries | 0 | 8,206 | 367 ms | [json](data/vercel-next-js-agents-md.json) |
| `apache/airflow` | AGENTS.md | 0.95 | commands | 1 | 6,410 | 425 ms | [json](data/apache-airflow-agents-md.json) |
| `langchain-ai/langchain` | AGENTS.md | 0.93 | boundaries | 1 | 5,723 | 476 ms | [json](data/langchain-ai-langchain-agents-md.json) |
| `anthropics/anthropic-cookbook` | CLAUDE.md | 0.91 | boundaries | 1 | 1,931 | 323 ms | [json](data/anthropics-anthropic-cookbook-claude-md.json) |
| `cloudflare/workers-sdk` | AGENTS.md | 0.89 | boundaries | 0 | 2,843 | 344 ms | [json](data/cloudflare-workers-sdk-agents-md.json) |
| `block/goose` | AGENTS.md | 0.86 | boundaries | 1 | 2,512 | 317 ms | [json](data/block-goose-agents-md.json) |
| `openai/codex` | AGENTS.md | 0.83 | boundaries | 2 | 6,285 | 392 ms | [json](data/openai-codex-agents-md.json) |
| `sst/opencode` | AGENTS.md | 0.72 | boundaries | 4 | 2,980 | 396 ms | [json](data/sst-opencode-agents-md.json) |
| `ollama/ollama` | AGENTS.md | 0.48 | testing | 9 | 1,048 | 430 ms | [json](data/ollama-ollama-agents-md.json) |
| `microsoft/vscode` | AGENTS.md | 0.28 | boundaries | 13 | 1,007 | 345 ms | [json](data/microsoft-vscode-agents-md.json) |
| `stanfordnlp/dspy` | none | None | | | 0 | | [json](data/stanfordnlp-dspy-not-found.json) |

Every row links to the run it came from in [`data/`](data/), which holds the eleven reports these numbers were read off.

The table rounds readiness to two places, and repeat runs moved each number by under 0.01: Airflow landed between 0.94 and 0.95 across five calls, which is why it and `vercel/next.js` both read 0.95, with 0.9471 and 0.9482 underneath.

**The score grades the file, not the project.** VS Code's AGENTS.md is 271 bytes and points at `.github/copilot-instructions.md` for everything. The check reads only the file you hand it, so 0.28 is right about that file and says nothing about the project behind it. Ollama's is 358 bytes of build commands, which is why it lands at 0.48.

**Nearly nobody documents nesting.** Eight of the ten files scored `nested_files` as `missing`, `cloudflare/workers-sdk` reaching `adequate` at 0.68 and `vercel/next.js` `weak` at 0.15. An agent cannot infer the precedence rule from prose about something else, so it is the cheapest thing on this page to fix.

**`boundaries` wins the weakest-area vote eight times out of ten.** Those files name commands, tests and style, then say nothing about what an agent must never do. Airflow is one of two exceptions, with explicit "Ask first" and "Never" lists, so its weakest area is commands. Ollama is the other, with testing.

## Wiring it into CI

The exit code and the JSON report are the two hooks. A run that finds no file exits 0 with `"readiness": null`, so a repo without an AGENTS.md fails your gate on the readiness bar rather than on a crash, and a leaked credential shows up as a `leaks_secret` judgement of `present`.

```bash
# in a pull request job, after checkout
uv run agents_md_readiness.py AGENTS.md
python - <<'PY'
import json, pathlib, sys

report = json.loads(pathlib.Path("data/your-repo-agents-md.json").read_text())
readiness = report["readiness"]
leak = report["questions"]["leaks_secret"]["judgement"]

if leak in {"present", "likely present", "suspect"}:
    sys.exit("AGENTS.md looks like it carries a credential")
if readiness is None:
    sys.exit("no AGENTS.md or CLAUDE.md in this repo")
if readiness < 0.80:
    sys.exit(f"AGENTS.md readiness {readiness:.2f} is under the 0.80 bar")
PY
```

Set the bar at 0.80 and four of the eleven repos above fail today: `microsoft/vscode` at 0.28, `ollama/ollama` at 0.48, `sst/opencode` at 0.72, and `stanfordnlp/dspy` with no file to read.

| CI threshold | Fails, out of 11 | Who fails |
| --- | --- | --- |
| 0.70 | 3 | vscode, ollama, dspy |
| 0.80 | 4 | vscode, ollama, opencode, dspy |
| 0.90 | 7 | vscode, ollama, opencode, codex, goose, workers-sdk, dspy |

A failing check puts the fix in front of the developer holding the branch, while they can still edit the file, instead of leaving AGENTS.md to rot until an agent guesses wrong in production. Each failure names the weakest area and lists the checks judged `missing`, so the fix is a paragraph rather than an investigation. Readiness becomes one more gate in the software factory, beside the linter, the type check and the test suite, at three cents a month and under half a second a run.

### One binary, no Python

A CI runner that has no Python can install the same check as a single static executable. [`go/`](go/) holds a Go port of this sample: it bakes in `questions.yml`, talks to the Jev API over HTTP, prints the same table, writes the same JSON, and adds two gates so the job needs no inline script.

```bash
curl -fsSL https://raw.githubusercontent.com/aarora79/jev-samples/main/samples/agents-md-readiness/go/install.sh | sh
agents-md-readiness -fail-under 0.8 -fail-on-credential AGENTS.md
```

Exit codes are 0 scored, 1 error, 2 gate failed. [v0.1.0](https://github.com/aarora79/jev-samples/releases/tag/agents-md-readiness/v0.1.0) ships binaries for linux and macOS on amd64 and arm64, plus windows amd64, and the installer checks them against the published `SHA256SUMS`. The Python here stays canonical, and a test in that folder fails when its copy of the payload drifts. [go/README.md](go/README.md) covers building, releasing and the caveats.

## What it asks

The [agents.md FAQ](https://agents.md) answers "are there required fields?" with "no", so nothing here checks a schema. The 17 questions cover the sections the format recommends, the properties that decide whether an agent can act on the file, and the one line you never want in a repo.

| Question id | Type | Asks |
| --- | --- | --- |
| `written_for` | `Choice` | Agent instructions, human documentation, or a stub? |
| `weakest_area` | `Choice` | Which of commands, testing, conventions, layout or boundaries to fix first? |
| `commands` | `Score` | How completely does it cover the commands an agent needs? |
| `testing` | `Score` | How completely does it cover running the tests? |
| `conventions` | `Score` | How specific are the code style rules? |
| `repo_map` | `Score` | How completely does it map the repository layout? |
| `project_overview` | `Noul` | Does it say what the project is and what it does? |
| `setup_commands` | `Noul` | Does it give commands to install dependencies? |
| `build_commands` | `Noul` | Does it give commands to build or run the project? |
| `test_commands` | `Noul` | Does it give commands to run the tests? |
| `code_style` | `Noul` | Does it state code style or naming rules? |
| `pr_rules` | `Noul` | Does it state how to write a commit message or a pull request? |
| `security_notes` | `Noul` | Does it cover security, secrets, or what never to commit? |
| `nested_files` | `Noul` | Does it explain nested AGENTS.md or CLAUDE.md files, and which one wins? |
| `boundaries` | `Noul` | Does it name actions to avoid, or ones needing a human first? |
| `runnable_commands` | `Noul` | Do commands appear as lines a reader could copy and run? |
| `leaks_secret` | `Noul` | Does a line look like a credential, API key, or token? |

Both filenames hold the same kind of document. Name a path or a GitHub file page and the sample reads that file whatever it is called. Give it a repo root or no argument and it tries `AGENTS.md`, then `CLAUDE.md`. The filename travels in the state, so Jev knows which convention it is reading.

Everything the sample sends sits in [`questions.yml`](questions.yml): the model pin, the state budget, the input-token price, and every question with its weight and the label it prints under. `agents_md_readiness.py` reads that file and does the judging.

Sixteen questions carry a `weight`, and readiness is the weighted average of what they returned. `weakest_area` carries none, because it routes the fix instead of grading the file.

| Weight | Questions |
| --- | --- |
| 0.15 | `leaks_secret` |
| 0.11, 0.09 | `commands`, `testing` |
| 0.08, 0.07 | `written_for`, `test_commands`, `security_notes`, `runnable_commands`, `conventions` |
| 0.06, 0.05, 0.04 | `boundaries`, `setup_commands`, `build_commands` |
| 0.03, 0.02 | `project_overview`, `code_style`, `pr_rules`, `repo_map`, `nested_files` |

A leaked credential outranks everything, because rotating a key and auditing a repo costs more than any missing section. Commands and tests come next, because an agent runs those lines. A repo map and nested-file precedence earn least, because an agent can list the tree itself and most repos ship one instruction file.

One entry looks like this. `leaks_secret` is the only inverted one, where a yes costs readiness instead of earning it:

```yaml
  leaks_secret:
    type: noul
    label: leaks a credential
    weight: 0.15
    invert: true
    instructions: The document contains a line that looks like a credential, API key, or access token
```

A `choice` entry carries a `credit` table naming what each option is worth: `written_for` pays 1.0 for `agent` and 0.0 for `human` or `stub`.

Add an eighteenth check as an eighteenth entry. The Python stays the same length, and readiness divides by whatever weights the file holds, so one edited weight needs no rebalancing.

## Prerequisites

- Python 3.11 or newer
- [uv](https://docs.astral.sh/uv/)
- A TypeSafe API key

## Run it

```bash
cd samples/agents-md-readiness

# The key comes from TYPESAFE_API_KEY, or from .env beside the sample or at the
# repo root. Export it to override the file.
export TYPESAFE_API_KEY="your-key"   # optional once ../../.env exists

# The AGENTS.md or CLAUDE.md in the current directory
uv run agents_md_readiness.py

# Any local file, whatever it is called
uv run agents_md_readiness.py ../../AGENTS.md
uv run agents_md_readiness.py ~/some-project/CLAUDE.md

# A repo root tries AGENTS.md, then CLAUDE.md
uv run agents_md_readiness.py https://github.com/apache/airflow
uv run agents_md_readiness.py https://github.com/anthropics/anthropic-cookbook

# A file page names the file itself
uv run agents_md_readiness.py https://github.com/anthropics/anthropic-cookbook/blob/main/CLAUDE.md

# The raw answers first, then the same summary
uv run agents_md_readiness.py --verbose https://github.com/apache/airflow
```

Output from a run against `https://github.com/apache/airflow` on 19 September 2026:

```text
AGENTS.md  (https://raw.githubusercontent.com/apache/airflow/HEAD/AGENTS.md)

  written for     agent        (confidence 1.00)
      Choice: one label out of 3,
      scored agent 1.00, stub 0.00, human 0.00.

  commands        2.0 / 2      (confidence 1.00)
      nearest level 2 "Exact commands, with the paths and flags needed to run them"
  testing         2.0 / 2      (confidence 0.97)
      nearest level 2 "Exact test commands, and what to do when a test fails"
  conventions     2.0 / 2      (confidence 1.00)
      nearest level 2 "Rules naming the tools, settings and patterns this project uses"
  repo map        2.0 / 2      (confidence 1.00)
      nearest level 2 "Lists the directories or key files and says what each one holds"

  agents.md checklist
    project overview         0.42   unsettled
    setup commands           0.99   yes
    build or run commands    0.99   yes
    test commands            0.99   yes
    code style rules         0.99   yes
    commit and PR rules      0.86   probably yes
    security notes           0.99   yes
    nested file precedence   0.04   no
    boundaries and asks      0.99   yes
    commands are runnable    0.95   yes
    leaks a credential       0.03   no
      Noul: one probability, and the number is the confidence. The agents.md FAQ
      says the format requires no fields, so read these as coverage rather than
      as a pass or a fail.

  weakest area    commands     (confidence 0.46)
      Choice: commands 0.60, boundaries 0.18, conventions 0.14, testing 0.05, layout 0.03.
      Jev ranks the five areas and names one even when every area is strong,
      so read this next to the readiness number rather than on its own.
```

The same call also prints one padded table. The padding makes it readable in a terminal, and the pipes keep it valid markdown, so the text pastes into a pull request:

```text
| Key                 | Label                  | Type            | Jev returned              | Weight | Credit | Judgement      |
| ------------------- | ---------------------- | --------------- | ------------------------- | ------ | ------ | -------------- |
| `written_for`       | written for            | choice          | agent, confidence 1.00    | 0.08   | 1.00   | strong         |
| `weakest_area`      | weakest area           | choice          | commands, confidence 0.46 |        |        | routes the fix |
| `commands`          | commands               | score           | 2.0 / 2, confidence 1.00  | 0.11   | 1.00   | strong         |
| `testing`           | testing                | score           | 2.0 / 2, confidence 0.97  | 0.09   | 0.99   | strong         |
| `conventions`       | conventions            | score           | 2.0 / 2, confidence 1.00  | 0.07   | 1.00   | strong         |
| `repo_map`          | repo map               | score           | 2.0 / 2, confidence 1.00  | 0.03   | 1.00   | strong         |
| `project_overview`  | project overview       | noul            | 0.42                      | 0.03   | 0.42   | thin, unsure   |
| `setup_commands`    | setup commands         | noul            | 0.99                      | 0.05   | 0.99   | strong         |
| `build_commands`    | build or run commands  | noul            | 0.99                      | 0.04   | 0.99   | strong         |
| `test_commands`     | test commands          | noul            | 0.99                      | 0.07   | 0.99   | strong         |
| `code_style`        | code style rules       | noul            | 0.99                      | 0.03   | 0.99   | strong         |
| `pr_rules`          | commit and PR rules    | noul            | 0.86                      | 0.03   | 0.86   | strong         |
| `security_notes`    | security notes         | noul            | 0.99                      | 0.07   | 0.99   | strong         |
| `nested_files`      | nested file precedence | noul            | 0.04                      | 0.02   | 0.04   | missing        |
| `boundaries`        | boundaries and asks    | noul            | 0.99                      | 0.06   | 0.99   | strong         |
| `runnable_commands` | commands are runnable  | noul            | 0.95                      | 0.07   | 0.95   | strong         |
| `leaks_secret`      | leaks a credential     | noul (inverted) | 0.03                      | 0.15   | 0.97   | clean          |

**Readiness 0.95 / 1.00**, the weighted average of the credit column over 16 questions carrying 1.00 of weight.
Weights live in questions.yml, so raise the one you care about and re-run.

17 questions, 6,410 input tokens, 425 ms, $0.00027 at $0.042 per million input tokens.
Report: /home/ubuntu/repos/jev-samples/samples/agents-md-readiness/data/apache-airflow-agents-md.json
```

The `Credit` column shows what each answer contributed, from 0 to 1: a Score over its top level, a Noul as its probability, one minus that probability for the inverted row, and the `credit` table for a Choice. `Judgement` reads the credit as a word, and appends "unsure" when a Choice or Score returns under 0.50 confidence or a Noul falls between 0.35 and 0.65.

Airflow's file is 22,071 characters, which is where most of those 6,410 tokens went. It loses its five points on `nested_files` and `project_overview`: the file never explains nesting, and it opens with Dag naming rules instead of saying what Airflow is.

## The JSON report

Every run writes one report into [`data/`](data/), named for the repo and the file it read: `apache-airflow-agents-md.json`, `anthropics-anthropic-cookbook-claude-md.json`, `stanfordnlp-dspy-not-found.json`. Each holds the source, the model, the token usage, the latency, the cost, the readiness, and one entry per question with Jev's raw answer beside the weight, credit and judgement the sample derived.

That folder holds the eleven committed reports behind the table at the top of this page. Re-run one of those repos and the new numbers land as a diff against the ones this README quotes, which is the cheapest calibration check available.

```bash
# rank everything in the folder, missing files last
jq -r '[.source, (.readiness|tostring)] | @tsv' data/*.json | sort -k2 -r

# name the checks one file failed outright
jq -r '.questions | to_entries[] | select(.value.judgement == "missing") | .key' data/apache-airflow-agents-md.json

# what the whole batch cost
cat data/*.json | jq -s '{tokens: ([.[]|select(.found)|.usage.input_tokens]|add), cost_usd: ([.[]|select(.found)|.cost_usd]|add)}'
```

Reproduce the table at the top, which overwrites the committed reports with your own:

```bash
for r in apache/airflow vercel/next.js langchain-ai/langchain anthropics/anthropic-cookbook \
         cloudflare/workers-sdk block/goose openai/codex sst/opencode ollama/ollama \
         microsoft/vscode stanfordnlp/dspy; do
  uv run agents_md_readiness.py "https://github.com/$r" > /dev/null
done
jq -r '[(.source | sub("https://raw.githubusercontent.com/"; "") | sub("/HEAD/.*"; "")),
        (.filename // "none"),
        (if .readiness == null then "None" else (.readiness | tostring) end)] | @tsv' data/*.json | sort -k3 -r
```

## When neither file exists

A repo holding neither name still produces a result, so the run reports the absence and a loop over twenty repos keeps going. From a run against `https://github.com/stanfordnlp/dspy` on 19 September 2026:

```text
no AGENTS.md or CLAUDE.md

  looked at       https://raw.githubusercontent.com/stanfordnlp/dspy/HEAD/AGENTS.md
  looked at       https://raw.githubusercontent.com/stanfordnlp/dspy/HEAD/CLAUDE.md

  readiness       None (not available)
      Nothing reached Jev, so no question ran and no readiness exists.
      A missing file has no score. Zero would mean it failed every check.

Report: /home/ubuntu/repos/jev-samples/samples/agents-md-readiness/data/stanfordnlp-dspy-not-found.json
```

The report carries `"readiness": null`, `"found": false` and the list of places it looked, so a batch run can tell a missing file from a bad one. Nothing reaches the API, so a missing file costs no tokens and needs no key. A local path that does not exist stays an error, since a wrong path is a typo.

`--verbose` prints the response as Jev returned it, before the sample reads a field off it.
`--debug` raises the log level, and `--help` lists the options.

## Either filename

`anthropics/anthropic-cookbook` ships a CLAUDE.md and no AGENTS.md, so the repo root tries the second name. From a run on 19 September 2026:

```text
INFO,Fetching: https://raw.githubusercontent.com/anthropics/anthropic-cookbook/HEAD/AGENTS.md
INFO,Not there: https://raw.githubusercontent.com/anthropics/anthropic-cookbook/HEAD/AGENTS.md
INFO,Fetching: https://raw.githubusercontent.com/anthropics/anthropic-cookbook/HEAD/CLAUDE.md

CLAUDE.md  (https://raw.githubusercontent.com/anthropics/anthropic-cookbook/HEAD/CLAUDE.md)

  written for     agent        (confidence 0.61)
      Choice: one label out of 3,
      scored agent 0.74, human 0.26, stub 0.00.
```

Only a 404 moves the sample to the next name, so a network error or a private repo still fails loudly. In a directory holding neither file, the run reports the absence and scores nothing.

That file scored `written_for` at 0.61 confidence with 0.26 on `human`, the lowest of any file checked that day. It opens with a paragraph aimed at a reader, then turns into agent instructions, and the split probability says so.

## The gates fire per action

Four verdicts guard four thresholds, each written next to the thing it protects. A six-line stub containing `export DEPLOY_TOKEN=ghp_...` produced this on 19 September 2026:

```text
    security notes           0.52   unsettled
    nested file precedence   0.03   no
    boundaries and asks      0.07   no
    commands are runnable    0.95   yes
    leaks a credential       0.99   yes

  -> read the file for a credential before you commit it
```

The credential gate fires at 0.3, because rotating a leaked key costs an afternoon and a false alarm costs one read. Missing tests fire at 0.5, the rewrite suggestion at 0.6 of the readiness range, and the wrong-audience verdict needs 0.7 confidence before it tells you your AGENTS.md reads like a README. Each threshold comes from the cost of being wrong, and sits beside the line it guards.

## What to notice

**Ask what the state can settle.** The first version of the credential check asked whether the document contains "a real credential, API key, or access token".
A file holding `sk-live-FAKE-NOT-A-REAL-KEY-0000000000` scored 0.05, because Jev read the word FAKE and answered the question I had asked.
Rewording it to "a line that looks like a credential, API key, or access token" moved the same file to 0.98, and a plausible-looking token to 0.99.
Jev takes text at face value, so a question it can settle from the state beats a question about the world behind the state.

**Seventeen questions cost one round trip.** Against Airflow's file, two questions ran 5,884 input tokens in 169 ms and sixteen ran 6,328 tokens in 162 ms, measured back to back on 19 September 2026.
The extra questions added tokens and no measurable time, because the state is the cost. The seventeenth, `repo_map`, took the request to 6,410 tokens.

**One question, one claim, or the number means two things.** `project_overview` first asked whether the document "says what the project is and how the repository is laid out".
Airflow scored 0.96 on that, carried by the layout half. Splitting the claim moved `project_overview` to 0.42 and put the layout in `repo_map`, which scores 2.0.
Airflow's AGENTS.md opens on naming rules and never says what Airflow is, so 0.42 is the truer number, and the folded question had been hiding it.

**The payload is data, and so are the weights.** `questions.yml` holds the model, the state budget, the price, the wording of all seventeen questions and what each one is worth. The four thresholds, the arithmetic and the layout sit in `agents_md_readiness.py`. Reviewing a new check means reading one YAML entry, and arguing about whether a repo map is worth 0.03 means reading one line.

**Sixteen weights beat one question.** Asking "how ready is this file" in a single question hides the weighting inside the model, where you cannot see it or change it.
Splitting the judgment across sixteen questions and weighting them puts every step in a diff.

**A low confidence marks a mixed document.** This repo's own AGENTS.md scored `testing` 1.6 at confidence 0.43 and `test_commands` 0.60, because the file lists commands to run and then says the repo has no test suite.
Both numbers point at the same sentence.

**Jev sees one file at a time.** Airflow scored `nested file precedence` 0.04, which is right about the document: it never explains nesting.
The repo does ship other AGENTS.md files, and no question over one file's text can know that.

## Two experiments worth running

Point it at ten AGENTS.md files you already know and read the output against the files themselves.
That is the calibration check: when Jev says 0.9, it should be right about nine times in ten on your material.

1. Change a weight in `questions.yml` and re-run the same files. Drop `leaks_secret` to 0.02 and watch a leaking file climb back to respectable, which is the argument for keeping it at 0.15.
2. Add a `noul` entry to `questions.yml` for a rule your team cares about, and time the call before and after. The state is the cost, so the wall clock holds about steady.

## Sources

- [agents.md](https://agents.md), read 19 September 2026, for the format, the recommended sections, the nested-file precedence rule, the "no required fields" FAQ answer, and the "over 60k open-source projects" figure. That count comes from [a GitHub code search](https://github.com/search?q=path%3AAGENTS.md+NOT+is%3Afork+NOT+is%3Aarchived&type=code) the site links, which I did not reproduce.
- [openai/agents.md](https://github.com/openai/agents.md) for the minimal example the format ships with.
- [apache/airflow/AGENTS.md](https://github.com/apache/airflow/blob/main/AGENTS.md) as the comprehensive example, and the ten other repos named in the table, each scored from the raw file on its default branch on 19 September 2026.
- TypeSafe's published price of $0.042 per million input tokens, September 2026, recorded in `questions.yml` as `input_usd_per_million`. Every latency here comes from this machine on that date, so treat it as one network's numbers rather than the vendor's.
