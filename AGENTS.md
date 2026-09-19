# AGENTS.md

Instructions for coding agents working in this repo. Humans can read it too.

## What this repo is

Runnable samples for Jev, TypeSafe AI's System One model. Each sample shows one pattern end to end with code that runs. A reader should be able to clone the repo, export a key, and get numbers back from their own account in a few minutes.

Samples are teaching material. Clarity beats cleverness, and a sample that needs a paragraph of explanation has failed.

## Layout

```
jev-samples/
  README.md               index of samples, prerequisites, the three primitives
  AGENTS.md               this file
  .env.example            TYPESAFE_API_KEY placeholder
  .scratchpad/            gitignored: plans, handoff notes, working documents
  samples/
    readme-check/         one folder per sample
      README.md           what it does, how to run it, what to notice
      pyproject.toml      its own dependencies
      readme_check.py     the sample
```

## Adding a sample

1. Create `samples/<sample-name>/` with kebab-case naming.
2. Give it its own `pyproject.toml`. Samples do not share a virtualenv, so one sample's dependency never becomes another's problem.
3. Write the sample as a single module where possible. Reach for a second file only when one stops being readable.
4. Write the sample README to answer three questions in this order: what it does, how to run it, what to notice.
5. Add a row to the table in the root README.
6. Run `uv run python -m py_compile <file>` before you call it done.

## Code conventions

- `uv` and `pyproject.toml` for everything. Never `pip`.
- Modern type hints: `str | None`, `list[dict]`, no `typing.Optional` or `typing.List`.
- One function parameter per line, two blank lines between functions.
- Private functions start with `_` and sit at the top of the file, public ones below.
- Functions stay under 30 to 50 lines. `main()` parses arguments and delegates.
- Constants at the top of the file, never buried in a function body.
- Multi-line imports.
- Logging config, every time:

```python
logging.basicConfig(
    level=logging.INFO,  # Set the log level to INFO
    # Define log message format
    format="%(asctime)s,p%(process)s,{%(filename)s:%(lineno)d},%(levelname)s,%(message)s",
)
```

- `argparse` with an `epilog` of example commands, plus a `--debug` flag that raises the log level.
- Never commit a key. Read `TYPESAFE_API_KEY` from the environment, and keep placeholders fake beyond doubt (`YOUR_TYPESAFE_API_KEY`).

## Jev conventions the samples must follow

These are the habits the samples exist to teach, so breaking one in a sample teaches the wrong thing.

- **Pin the model.** `MODEL = "jev-1.13.0"` as a module constant. The SDK defaults to `jev-latest`, which moves under you.
- **State is an object with named fields.** Name each part, so the model knows where one ends and the next begins.
- **Truncate the state** and say why in a comment. Padding costs accuracy as well as money.
- **One question, one thing.** Never fold two claims into one question.
- **Gate per action, not per system.** Put the threshold next to the action it guards, and pick the number from the cost of being wrong.
- **Weight composite judgments in Python**, so the weights live in a diff.
- **Do arithmetic in Python.** Jev cannot count, and its date and number comparisons fail.
- **Treat state built from user input as untrusted.** Text written to argue for its own classification moves the answer.

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
- No Claude Code attribution or generated-with footers in commits or pull requests.

## Scratchpad

`.scratchpad/` is gitignored. Plans, session notes and handoff documents go there, never in the repo proper. Start a session by reading `.scratchpad/HANDOFF.md`, and update it before you finish.
