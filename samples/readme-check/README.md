# readme-check

Read a README, ask Jev five questions about it, print the answers. One call, one round trip, no parsing.

The five questions use all three of Jev's primitives:

| Question id | Type | Asks |
| --- | --- | --- |
| `audience` | `Choice` | Is this written for a user, a contributor or an evaluator? |
| `setup` | `Score` | How complete are the setup instructions, on a three-level rubric? |
| `has_example` | `Noul` | Does the document show at least one worked example? |
| `explains_auth` | `Noul` | Does it explain how to authenticate? |
| `sounds_stale` | `Noul` | Does it mention versions or features that sound out of date? |

## Prerequisites

- Python 3.11 or newer
- [uv](https://docs.astral.sh/uv/)
- A TypeSafe API key

## Run it

```bash
export TYPESAFE_API_KEY="your-key"   # or: set -a && . ./.env && set +a

cd samples/readme-check

# The README in the current directory
uv run readme_check.py

# Any local file
uv run readme_check.py ../../README.md

# A GitHub repo root or a file page
uv run readme_check.py https://github.com/psf/requests
```

Output, from a real run against `https://github.com/psf/requests` on 19 September 2026:

```text
README.md
  written for     user         (0.98)
  setup steps     1.7 / 2
  worked example  0.99
  auth explained  0.85
  sounds stale    0.30
```

That call took 327 ms end to end, inside the 70 to 500 ms TypeSafe quotes but nowhere near the 100 ms they describe as typical. Log lines carry the request id, so keep them when you report a latency number.

`--debug` turns on debug logging. `--help` lists everything.

## What to notice

**The state is an object, not a blob.** `{"filename": ..., "readme": ...}` tells the model what each part is, and named fields let you diff one state against another the first time an answer surprises you.

**The document is truncated at 40,000 characters**, roughly 10,000 tokens. That keeps five questions well inside the 32,000-token per-question limit, and state you did not need for the question costs accuracy as well as money.

**The model is pinned** to `jev-1.13.0`. The SDK defaults to `jev-latest`, so any threshold you tune against measured behavior would drift under a silent upgrade.

**A `Score` lands between levels.** `1.4` on the three-level rubric sits between "steps exist but assume things they never state" and "a reader could follow them start to finish". Treat the rubric as a ruler rather than three boxes.

**A `Noul` has no separate confidence.** The probability is the confidence.

## Two experiments worth running

Point it at twenty READMEs you already know well and read the output against what is in the files. You are testing calibration on your own material: when Jev says 0.9, it should be right about nine times in ten.

1. Add a sixth question and watch the wall clock barely move. That is fan-out: the state is the cost, and questions are cheap.
2. Change `MODEL` to `jev-latest`, re-run the same files, and see what the pin was protecting.
