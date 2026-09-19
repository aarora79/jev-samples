# agents-md-readiness

Reads an AGENTS.md or a CLAUDE.md from disk or a GitHub URL, asks Jev seventeen questions about it in one call, and prints each answer with a line explaining the number next to it.

Both names hold the same kind of file, so the sample takes either. Name a path or a GitHub file page and it reads that file whatever it is called. Give it a repo root or no argument and it tries `AGENTS.md` first, then `CLAUDE.md`. The filename travels in the state, so Jev knows which convention it is reading.

The [agents.md FAQ](https://agents.md) answers "are there required fields?" with "no", so nothing here checks a schema.
The seventeen questions cover the sections the format recommends, the properties that decide whether an agent can act on the file, and the one line you never want in a repo.

Everything the sample sends sits in [`questions.yml`](questions.yml): the model pin, the state budget, and every question with the label it prints under.
`agents_md_readiness.py` reads that file and does the judging.

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
| `nested_files` | `Noul` | Does it explain nested AGENTS.md files and which one wins? |
| `boundaries` | `Noul` | Does it name actions to avoid, or ones needing a human first? |
| `runnable_commands` | `Noul` | Do commands appear as lines a reader could copy and run? |
| `leaks_secret` | `Noul` | Does a line look like a credential, API key, or token? |

Sixteen of the seventeen carry a `weight`, and readiness is the weighted average of what they returned. `weakest_area` carries none: it routes the fix rather than grading the file.

The weights say what a gap costs, so they belong in the payload beside the questions:

| Weight | Questions |
| --- | --- |
| 0.15 | `leaks_secret` |
| 0.11, 0.09 | `commands`, `testing` |
| 0.08, 0.07 | `written_for`, `test_commands`, `security_notes`, `runnable_commands`, `conventions` |
| 0.06, 0.05, 0.04 | `boundaries`, `setup_commands`, `build_commands` |
| 0.03, 0.02 | `project_overview`, `code_style`, `pr_rules`, `repo_map`, `nested_files` |

A leaked credential outranks everything, because rotating a key and auditing a repo costs more than any missing section. Commands and tests come next, because an agent runs those lines. A repo map and nested-file precedence sit at the bottom, because an agent can list the tree itself and most repos ship one instruction file.

One entry looks like this, and `leaks_secret` is the only inverted one, where a yes costs readiness instead of earning it:

```yaml
  leaks_secret:
    type: noul
    label: leaks a credential
    weight: 0.15
    invert: true
    instructions: The document contains a line that looks like a credential, API key, or access token
```

A `choice` entry carries a `credit` table instead, naming what each option is worth: `written_for` pays 1.0 for `agent` and 0.0 for `human` or `stub`.

Adding an eighteenth check means adding an eighteenth entry. The Python stays the same length, and the readiness divides by whatever weights it finds, so one edited weight needs no rebalancing.

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
    project overview         0.48   unsettled
    setup commands           0.99   yes
    build or run commands    0.99   yes
    test commands            0.99   yes
    code style rules         0.99   yes
    commit and PR rules      0.87   probably yes
    security notes           0.99   yes
    nested file precedence   0.04   no
    boundaries and asks      0.99   yes
    commands are runnable    0.95   yes
    leaks a credential       0.03   no
      Noul: one probability, and the number is the confidence. The agents.md FAQ
      says the format requires no fields, so read these as coverage rather than
      as a pass or a fail.

  weakest area    commands     (confidence 0.50)
      Choice: commands 0.60, boundaries 0.18, conventions 0.14, testing 0.05, layout 0.03.
      Jev ranks the five areas and names one even when every area is strong,
      so read this next to the readiness number rather than on its own.
```

Then the same call as one padded table. The padding makes it readable in a terminal, and the pipes keep it valid markdown, so the same text pastes into a pull request:

```text
| Key                 | Label                  | Type            | Jev returned              | Weight | Credit | Judgement      |
| ------------------- | ---------------------- | --------------- | ------------------------- | ------ | ------ | -------------- |
| `written_for`       | written for            | choice          | agent, confidence 1.00    | 0.08   | 1.00   | strong         |
| `weakest_area`      | weakest area           | choice          | commands, confidence 0.43 |        |        | routes the fix |
| `commands`          | commands               | score           | 2.0 / 2, confidence 1.00  | 0.11   | 1.00   | strong         |
| `testing`           | testing                | score           | 2.0 / 2, confidence 0.97  | 0.09   | 0.99   | strong         |
| `conventions`       | conventions            | score           | 2.0 / 2, confidence 1.00  | 0.07   | 1.00   | strong         |
| `repo_map`          | repo map               | score           | 2.0 / 2, confidence 1.00  | 0.03   | 1.00   | strong         |
| `project_overview`  | project overview       | noul            | 0.41                      | 0.03   | 0.41   | thin, unsure   |
| `setup_commands`    | setup commands         | noul            | 0.99                      | 0.05   | 0.99   | strong         |
| `build_commands`    | build or run commands  | noul            | 0.99                      | 0.04   | 0.99   | strong         |
| `test_commands`     | test commands          | noul            | 0.99                      | 0.07   | 0.99   | strong         |
| `code_style`        | code style rules       | noul            | 0.99                      | 0.03   | 0.99   | strong         |
| `pr_rules`          | commit and PR rules    | noul            | 0.85                      | 0.03   | 0.85   | strong         |
| `security_notes`    | security notes         | noul            | 0.99                      | 0.07   | 0.99   | strong         |
| `nested_files`      | nested file precedence | noul            | 0.04                      | 0.02   | 0.04   | missing        |
| `boundaries`        | boundaries and asks    | noul            | 0.99                      | 0.06   | 0.99   | strong         |
| `runnable_commands` | commands are runnable  | noul            | 0.95                      | 0.07   | 0.95   | strong         |
| `leaks_secret`      | leaks a credential     | noul (inverted) | 0.04                      | 0.15   | 0.96   | clean          |

**Readiness 0.94 / 1.00**, the weighted average of the credit column over 16 questions carrying 1.00 of weight.
Weights live in questions.yml, so raise the one you care about and re-run.

Report: /home/ubuntu/repos/jev-samples/samples/agents-md-readiness/apache-airflow-agents-md.json
```

The `Credit` column is what each answer contributed, from 0 to 1: a Score over its top level, a Noul as its probability, one minus that probability for the inverted row, and the `credit` table for a Choice. `Judgement` reads the credit as a word, and appends "unsure" when a Choice or Score came back under 0.50 confidence or a Noul landed between 0.35 and 0.65.

That call carried 6,410 input tokens and took 383 ms end to end, with `x-envoy-upstream-service-time` reporting 152 ms inside TypeSafe.
Airflow's file is 22,071 characters, which is where most of those tokens went.

Airflow loses its six points on `nested_files` and `project_overview`: it never explains nesting, and it opens on Dag naming rules rather than saying what Airflow is.

## The JSON report

Every run writes one report into [`data/`](data/), named for the repo and the file it read: `apache-airflow-agents-md.json`, `anthropics-anthropic-cookbook-claude-md.json`, `stanfordnlp-dspy-not-found.json`. It holds the source, the model, the token usage, the readiness, and one entry per question carrying the raw answer Jev returned next to the weight, credit and judgement the sample derived.

The eleven reports in that folder are committed, because they are the runs behind the table below. Re-run one of those repos and the new numbers show up as a diff against the ones this README quotes, which is the cheapest calibration check available.

```bash
# rank everything in the folder, missing files last
jq -r '[.source, (.readiness|tostring)] | @tsv' data/*.json | sort -k2 -r

# name the checks one file failed outright
jq -r '.questions | to_entries[] | select(.value.judgement == "missing") | .key' data/apache-airflow-agents-md.json
```

## Scores from eleven open-source repos

One run each on 19 September 2026, against whatever the default branch held that day, sorted by readiness. Every row links to the report it came from in [`data/`](data/). `Weakest area` is the `weakest_area` Choice, and `Missing` counts the checks judged `missing` out of sixteen.

| Repo | File | Readiness | Weakest area | Missing | Report |
| --- | --- | --- | --- | --- | --- |
| `vercel/next.js` | AGENTS.md | 0.95 | boundaries | 0 | [json](data/vercel-next-js-agents-md.json) |
| `apache/airflow` | AGENTS.md | 0.95 | commands | 1 | [json](data/apache-airflow-agents-md.json) |
| `langchain-ai/langchain` | AGENTS.md | 0.93 | boundaries | 1 | [json](data/langchain-ai-langchain-agents-md.json) |
| `anthropics/anthropic-cookbook` | CLAUDE.md | 0.91 | boundaries | 1 | [json](data/anthropics-anthropic-cookbook-claude-md.json) |
| `cloudflare/workers-sdk` | AGENTS.md | 0.89 | boundaries | 0 | [json](data/cloudflare-workers-sdk-agents-md.json) |
| `block/goose` | AGENTS.md | 0.86 | boundaries | 1 | [json](data/block-goose-agents-md.json) |
| `openai/codex` | AGENTS.md | 0.83 | boundaries | 3 | [json](data/openai-codex-agents-md.json) |
| `sst/opencode` | AGENTS.md | 0.72 | boundaries | 4 | [json](data/sst-opencode-agents-md.json) |
| `ollama/ollama` | AGENTS.md | 0.48 | testing | 9 | [json](data/ollama-ollama-agents-md.json) |
| `microsoft/vscode` | AGENTS.md | 0.28 | boundaries | 13 | [json](data/microsoft-vscode-agents-md.json) |
| `stanfordnlp/dspy` | none | None | | | [json](data/stanfordnlp-dspy-not-found.json) |

Readiness is rounded to two places, and repeat runs moved each number by under 0.01: Airflow landed between 0.94 and 0.95 across five calls, which is why it and `vercel/next.js` tie here at 0.95 with 0.9476 and 0.9482 underneath.

Three things fall out of that column of numbers.

**The score grades the file, not the project.** VS Code's AGENTS.md is 271 bytes and points at `.github/copilot-instructions.md` for everything. The check reads the file it was given, so 0.28 is right about the file and says nothing about the project behind it. Ollama's is 358 bytes of build commands, which is why it lands at 0.48.

**Nearly nobody documents nesting.** Eight of the ten files that exist scored `nested_files` as `missing`, `cloudflare/workers-sdk` reaching `adequate` at 0.68 and `vercel/next.js` `weak` at 0.15. The precedence rule is the one part of the format an agent cannot work out from the text, so it is the cheapest thing on this page to fix.

**`boundaries` wins the weakest-area vote eight times out of ten.** Those files name commands, tests and style, then stop short of saying what an agent must never do. Airflow is one of the two exceptions: it carries explicit "Ask first" and "Never" lists, so its weakest area is commands instead. Ollama is the other, with testing.

Reproduce the whole table, which overwrites the committed reports with your own:

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

A repo holding neither name is an answer rather than an error, so the run reports it and keeps a loop over twenty repos going. From a run against `https://github.com/stanfordnlp/dspy` on 19 September 2026:

```text
no AGENTS.md or CLAUDE.md

  looked at       https://raw.githubusercontent.com/stanfordnlp/dspy/HEAD/AGENTS.md
  looked at       https://raw.githubusercontent.com/stanfordnlp/dspy/HEAD/CLAUDE.md

  readiness       None (not available)
      Nothing reached Jev, so no question ran and no readiness exists.
      None is not zero: zero would say the document failed every check.

Report: /home/ubuntu/repos/jev-samples/samples/agents-md-readiness/stanfordnlp-dspy-not-found.json
```

The report carries `"readiness": null`, `"found": false` and the list of places it looked, so a batch run can tell a missing file from a bad one. Nothing reaches the API, so a missing file costs no tokens and needs no key. A local path that does not exist is still an error, because that is a typo rather than a finding.

`--verbose` prints the response as Jev returned it, before the sample reads a field off it.
`--debug` raises the log level, and `--help` lists the options.

## Either filename

`anthropics/anthropic-cookbook` ships a CLAUDE.md and no AGENTS.md, so the repo root falls through to the second name. From a run on 19 September 2026:

```text
INFO,Fetching: https://raw.githubusercontent.com/anthropics/anthropic-cookbook/HEAD/AGENTS.md
INFO,Not there: https://raw.githubusercontent.com/anthropics/anthropic-cookbook/HEAD/AGENTS.md
INFO,Fetching: https://raw.githubusercontent.com/anthropics/anthropic-cookbook/HEAD/CLAUDE.md

CLAUDE.md  (https://raw.githubusercontent.com/anthropics/anthropic-cookbook/HEAD/CLAUDE.md)

  written for     agent        (confidence 0.60)
      Choice: one label out of 3,
      scored agent 0.73, human 0.27, stub 0.00.

  readiness       0.89 / 1.00
```

Only a 404 moves the sample to the next name, so a network error or a private repo still fails loudly.
In a directory holding neither file, it stops with `no AGENTS.md or CLAUDE.md in this directory: name a file to check`.

That file scored `written_for` at 0.60 confidence with 0.27 on `human`, the lowest of any file checked today. It opens with a paragraph about what the cookbook is for a reader, then turns into agent instructions, and the split probability says so.

## The gates fire per action

Four verdicts sit at four thresholds, each written next to the thing it guards.
A six-line stub with `export DEPLOY_TOKEN=ghp_...` in it produced this on 19 September 2026:

```text
    security notes           0.52   unsettled
    nested file precedence   0.03   no
    boundaries and asks      0.07   no
    commands are runnable    0.95   yes
    leaks a credential       0.99   yes

  -> read the file for a credential before you commit it
```

The credential gate fires at 0.3, because rotating a leaked key costs an afternoon and a false alarm costs one read.
The missing-tests gate sits at 0.5, the rewrite suggestion at 0.6 of the readiness range, and the wrong-audience gate needs 0.7 confidence before it tells you your AGENTS.md reads like a README.
Pick each number from what being wrong costs you, and keep it next to the line it guards.

## What to notice

**Ask about the text, not about the world.** The first version of the credential check asked whether the document contains "a real credential, API key, or access token".
A file holding `sk-live-FAKE-NOT-A-REAL-KEY-0000000000` scored 0.05, because Jev read the word FAKE and answered honestly.
Rewording it to "a line that looks like a credential, API key, or access token" moved the same file to 0.98, and a plausible-looking token to 0.99.
Jev reads literally, so a question it can settle from the state beats a question about the world behind the state.

**Seventeen questions cost one round trip.** Against Airflow's file, two questions ran 5,884 input tokens in 169 ms and sixteen ran 6,328 tokens in 162 ms, measured back to back on 19 September 2026.
The extra questions added tokens and no measurable time, because the state is the cost. The seventeenth, `repo_map`, took the request to 6,402 tokens.

**One question, one claim, or the number means two things.** `project_overview` first asked whether the document "says what the project is and how the repository is laid out".
Airflow scored 0.96 on that, carried by the layout half. Splitting the claim moved `project_overview` to 0.44 and put the layout in `repo_map`, which scores 2.0.
Airflow's AGENTS.md opens on naming rules and never says what Airflow is, so 0.44 is the truer number, and the folded question had been hiding it.

**The payload is data, and so are the weights.** `questions.yml` holds the model, the state budget, the wording of all seventeen questions and what each one is worth. `agents_md_readiness.py` holds the four thresholds, the arithmetic and the layout. Reviewing a new check means reading one YAML entry, and arguing about whether a repo map is worth 0.03 means reading one line.

**Sixteen weights beat one question.** Asking "how ready is this file" in a single question hides the weighting inside the model, where you cannot see it or change it.
Splitting the judgment across sixteen questions and weighting them puts every step in a diff.

**A low confidence marks a genuinely mixed document.** This repo's own AGENTS.md scored `testing` 1.6 at confidence 0.43 and `test_commands` 0.60, because the file lists commands to run and then says there is no test suite.
Both numbers point at the same sentence.

**Jev sees the file, not the tree.** Airflow scored `nested file precedence` 0.03, which is right about the document: it never explains nesting.
The repo does ship other AGENTS.md files, and no question over one file's text can know that.

## Two experiments worth running

Point it at ten AGENTS.md files you already know and read the output against what is in them.
That is the calibration check: when Jev says 0.9, it should be right about nine times in ten on your material.

1. Change a weight in `questions.yml` and re-run the same files. Drop `leaks_secret` to 0.02 and watch a leaking file climb back to respectable, which is the argument for keeping it at 0.15.
2. Add a `noul` entry to `questions.yml` for a rule your team cares about, and time the call before and after. The state is the cost, so the wall clock holds about steady.
