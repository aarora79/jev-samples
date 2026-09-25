# AGENTS.md

Context for AI coding agents working in this repo, in the format described at <https://agents.md>. Humans can read it too.

## Project overview

Runnable samples for Jev, TypeSafe AI's System One model. Each sample shows one pattern end to end with code that runs. A reader should be able to clone the repo, export a key, and get numbers back from their own account in a few minutes.

Samples are teaching material. Clarity beats cleverness, and a sample that needs a paragraph of explanation has failed.

```
jev-samples/
  README.md               index of samples, a curl walkthrough, the three primitives
  AGENTS.md               this file
  .env.example            TYPESAFE_API_KEY placeholder
  .scratchpad/            gitignored: plans, session notes, working documents
  samples/
    readme-check/         one folder per sample
      README.md           what it does, how to run it, what to notice
      pyproject.toml      its own dependencies
      questions.yml       the Jev payload: model pin, state budget, questions
      readme_check.py     the sample
    agents-md-readiness/  the second sample
      go/                 a Go port of that sample, one static binary for CI
    pr-triage/            the third sample, one Jev call per pull request
      fetch_prs.py        builds the dataset from the GitHub API, needs no Jev key
      pr_triage.py        the Jev call, the tiers, the tables
      data/               triage reports, committed; the datasets they scored, gitignored
```

## Setup and commands

Each sample folder is its own uv project, so the root needs no install and samples never share a virtualenv.

```bash
# Samples read TYPESAFE_API_KEY from the environment, then fall back to .env
# beside the sample or at the repo root. Export it to override the file.
export TYPESAFE_API_KEY="..."   # optional once .env exists

cd samples/readme-check
uv run readme_check.py --help     # run a sample

uv run python -m py_compile readme_check.py   # compile check
uvx ruff check --fix . && uvx ruff format .   # lint and format
```

`ruff format` collapses a signature that fits on one line. To keep one parameter per line, leave a trailing comma after the last parameter, which ruff treats as a request to stay expanded.

## Testing instructions

One check runs in CI: `.github/workflows/agents-md-readiness.yml` scores this file on any pull request that touches it, and fails below 0.85 readiness or on a line that looks like a credential. The repo has no test suite, so a change is done when all of this passes:

1. `uv run python -m py_compile <file>` on every Python file you touched.
2. `uvx ruff check .` and `uvx ruff format --check .` both report clean.
3. The sample runs against a live key, and every code path you changed runs at least once. For readme-check that means a local file, a GitHub repo root, a `/blob/` file page, and an `http://` URL it should refuse. For agents-md-readiness add a repo holding neither AGENTS.md nor CLAUDE.md, and a directory with neither. For pr-triage that means a live fetch, a `--dataset` replay, `--explain` on a number in the dataset and on one that is absent, and a repo whose pull requests include a diff too large to send whole.
4. Any output shown in a README comes from a run you just did, with the date next to it.
5. `samples/agents-md-readiness/go/` compiles that sample into one static binary, so a change to its `questions.yml` or its Go files means `gofmt -l .`, `go vet ./...`, `go test ./...` and `./build.sh <version>` in that folder, then one live run of the binary you built. The Python stays canonical, and `payload_test.go` fails when the embedded copy of the payload drifts.

Run the whole list before you open a pull request, because CI covers the AGENTS.md check and nothing else. If you add a test suite, use pytest, mock the client rather than calling the API, and wire it into that workflow.

## Code style

- `uv` and `pyproject.toml` for everything. Never `pip`.
- Modern type hints: `str | None`, `list[dict]`, no `typing.Optional` or `typing.List`.
- One function parameter per line, two blank lines between functions.
- Private functions start with `_` and sit at the top of the file, public ones below.
- Functions stay under 30 to 50 lines. `main()` parses arguments and delegates.
- Constants at the top of the file, never buried in a function body.
- Multi-line imports.
- `argparse` with an `epilog` of example commands, plus a `--debug` flag that raises the log level.
- Logging config, every time:

```python
logging.basicConfig(
    level=logging.INFO,  # Set the log level to INFO
    # Define log message format
    format="%(asctime)s,p%(process)s,{%(filename)s:%(lineno)d},%(levelname)s,%(message)s",
)
```

## Jev conventions the samples must follow

These are the habits the samples exist to teach, so breaking one in a sample teaches the wrong thing.

- **The payload lives in `questions.yml`,** never in the Python. The file holds the model pin, the state budget and every question with its display label. The Python reads it, builds the state and judges the answers.
- **Pin the model** in that file: `model: jev-1.13.0`. The SDK defaults to `jev-latest`, which moves under you.
- **State is an object with named fields.** Name each part, so the model knows where one ends and the next begins.
- **Truncate the state** through `max_state_chars` and say why in a YAML comment. Padding costs accuracy as well as money.
- **One question, one thing.** Never fold two claims into one question.
- **Gate per action, not per system.** Put the threshold next to the action it guards, and pick the number from the cost of being wrong.
- **Weight composite judgments outside the model.** Put the weight for each question in `questions.yml` next to the question, and do the arithmetic in Python. Both sit in a diff, and neither hides inside a question's wording.
- **Do arithmetic in Python.** Jev cannot count, and its date and number comparisons fail.
- **Treat state built from user input as untrusted.** Text written to argue for its own classification moves the answer.

## Adding a sample

1. Create `samples/<sample-name>/` with kebab-case naming.
2. Give it its own `pyproject.toml`, depending on `typesafe-sdk` and `pyyaml`.
3. Put the whole Jev payload in `questions.yml`: `model`, the state budget, then `questions`, each entry carrying `type`, `label`, `instructions` and any `criteria`. Question order in the file is print order. One state field takes one `max_state_chars`; a state with several fields takes one budget per field, as `pr-triage` does, so a long diff cannot crowd out a description.
4. Write the sample as a single module where possible. Reach for a second file only when one stops being readable.
5. Write the sample README to answer three questions in this order: what it does, how to run it, what to notice.
6. Add a row to the table in the root README.
7. Run the checks under "Testing instructions".

If a sample needs rules of its own, add `samples/<sample-name>/AGENTS.md`. Agents read the nearest file up the tree, so it wins for files in that folder.

## Writing style

Every user-facing markdown file in this repo follows the writing skill: <https://github.com/aarora79/my-ai-assets/blob/main/skills/writing/SKILL.md>. Read it before you write or edit a README, and run its revision pass before you commit. That covers the root README, every sample README, and anything else a reader outside the team will see.

The rules the drafts here break most often:

- **Passive voice.** Name the actor. "The sample pins the model", not "the model is pinned".
- **-ly adverbs.** Pick a stronger verb. `significantly improved` becomes `doubled`.
- **Corrective negation.** Do not define a thing by what it is not. "The state is an object with named fields", not "the state is an object, not a blob".
- **Antithesis and contrasting pairs** for rhythm: "X, but Y", "not just X, but Y".
- **Parataxis.** Stacked short clauses that build a beat: "One call. One round trip. No parsing."
- **Setup sentences** whose only job is to introduce the next one, and summary beats that restate what you just said.
- **Padding.** in order to, the fact that, it is important to note, basically, very, simply, just.

Repo specifics on top of the skill:

- No emojis anywhere: code, comments, docs, log messages, commit messages.
- No em-dashes, and no double hyphens standing in for one. Use a comma, a colon, parentheses, or two sentences.
- Do not hard-wrap markdown. One sentence or paragraph per line, and let the editor soft-wrap.
- Attribute and date every vendor number. TypeSafe's latency and accuracy figures are TypeSafe's, and nobody outside the company has reproduced the latency.
- Prefer a recorded run to an invented one. If a README shows output, say when it ran and against what.

## Security

- Never commit a key. `.env` is gitignored, and placeholders stay fake beyond doubt (`YOUR_TYPESAFE_API_KEY`).
- Never put a real key in a README, a log line, a commit message, or a shell history you paste back. A sample may log the path a key came from, never the key.
- Fetch over https only. The sample refuses an `http://` target rather than pulling a document in the clear.
- Treat state built from user input as untrusted, the same way you treat a prompt.

## Commits and pull requests

- Say what changed, what you verified, and what breaks. Name the numbers.
- Run the checks under "Testing instructions" first.
- No Claude Code attribution, and no generated-with footers.

## Scratchpad

`.scratchpad/` is gitignored. Plans, session notes and working documents go there, never in the repo proper.
