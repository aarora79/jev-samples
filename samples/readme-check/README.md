# readme-check

Reads a README from disk or a GitHub URL, asks Jev five questions about it in one call, and prints each answer with a line explaining the number next to it.

Everything the sample sends sits in [`questions.yml`](questions.yml): the model pin, the state budget, and five questions, each with the label it prints under.
`readme_check.py` reads that file, fetches the document, and does the judging.

| Question id | Type | Asks |
| --- | --- | --- |
| `audience` | `Choice` | Is this written for a user, a contributor or an evaluator? |
| `setup` | `Score` | How complete are the setup instructions, on a three-level rubric? |
| `has_example` | `Noul` | Does the document show at least one worked example? |
| `explains_auth` | `Noul` | Does it explain how to authenticate? |
| `sounds_stale` | `Noul` | Does it mention versions or features that sound out of date? |

One entry looks like this:

```yaml
  setup:
    type: score
    label: setup steps
    instructions: How complete the setup instructions are
    criteria:
      - No install or setup steps at all
      - Steps exist but assume things they never state
      - A reader could follow them start to finish
```

Add a sixth question to that file and the Python never grows.

## Prerequisites

- Python 3.11 or newer
- [uv](https://docs.astral.sh/uv/)
- A TypeSafe API key

## Run it

```bash
cd samples/readme-check

# The key comes from TYPESAFE_API_KEY, or from .env beside the sample or at the
# repo root. Export it to override the file.
export TYPESAFE_API_KEY="your-key"   # optional once ../../.env exists

# The README in the current directory
uv run readme_check.py

# Any local file
uv run readme_check.py ../../README.md

# A GitHub repo root or a file page
uv run readme_check.py https://github.com/psf/requests

# The raw answers first, then the same summary
uv run readme_check.py --verbose https://github.com/psf/requests
```

Output from a run against `https://github.com/psf/requests` on 19 September 2026:

```text
README.md  (https://raw.githubusercontent.com/psf/requests/HEAD/README.md)

  written for     user         (confidence 0.98)
      Choice: one label out of 3, scored user 0.99, evaluator 0.01, contributor 0.00.
      Confidence 0.98 rates that pick, and Jev reports it
      apart from the spread, so the two numbers can differ.

  setup steps     1.7 / 2      (confidence 0.60)
      Score: between level 1 "Steps exist but assume things they never state"
      and level 2 "A reader could follow them start to finish".
      Jev weights the levels by probability (0 0.00, 1 0.27, 2 0.73), so the score lands between two of them.

  worked example  0.99         yes
  auth explained  0.85         probably yes
  sounds stale    0.29         probably no
      Noul: one probability, which is also the confidence. Near 0.50 says the
      document argues both ways, or never addresses the statement at all.
```

Every explanation line comes out of the answer itself: the probabilities Jev spread across the options, the legend it returns beside a Score, and the confidence it reports for both. The sample invents no numbers.

The header names the document Jev read. A GitHub repo root shows up as the raw URL the fetch used, and a relative path as an absolute one, so the line still says which file produced these numbers a week later.

A call in the same minute took 235 ms end to end, and its `x-envoy-upstream-service-time` header reported 54 ms inside TypeSafe, under the 100 ms they call typical. Calls from this machine landed between 235 and 340 ms across the day, so most of the time is transit. Run with `--debug` to see the header and the token count (1,252 input tokens on that call), and quote both numbers when you report latency.

`--help` lists the options.

## The raw answers

`--verbose` prints the response as Jev returned it, before the sample reads a field off it. From a run against `https://github.com/psf/requests` on 19 September 2026:

```json
{
  "model": "jev-1.13.0",
  "usage": {
    "input_tokens": 1252,
    "output_tokens": 110
  },
  "answers": {
    "audience": {
      "type": "choice",
      "choice": "user",
      "confidence": 0.98,
      "probabilities": {
        "user": 0.99,
        "evaluator": 0.01,
        "contributor": 0.0
      }
    },
    "setup": {
      "type": "score",
      "score": 1.71,
      "confidence": 0.57,
      "legend": {
        "0": "No install or setup steps at all",
        "1": "Steps exist but assume things they never state",
        "2": "A reader could follow them start to finish"
      },
      "probabilities": {
        "0": 0.0,
        "1": 0.29,
        "2": 0.71
      }
    },
    "has_example": {
      "type": "noul",
      "noul": 0.99
    },
    "explains_auth": {
      "type": "noul",
      "noul": 0.84
    },
    "sounds_stale": {
      "type": "noul",
      "noul": 0.31
    }
  }
}
```

The score is 1.71 and the summary rounds it to 1.7, so compare against the raw number when you set a threshold near a level boundary. Every `legend` and `probabilities` key arrives as a string on the wire, and the SDK hands them back keyed by `int`, which is why `answer.legend[1]` works and `answer.legend["1"]` raises `KeyError`. TypeSafe bills input tokens only, and it still counted 110 output tokens on this call.

## What to notice

**The payload is data and the judgment is code.** `questions.yml` holds the model, the state budget and the wording of every question. The two thresholds and the arithmetic sit in `readme_check.py`. A reviewer can read a question change without reading Python, and a threshold change shows up in a diff next to the action it guards.

**The state is an object with named fields.** `{"filename": ..., "readme": ...}` tells the model what each part is, and named fields let you diff one state against another the first time an answer surprises you.

**The payload truncates the document at 40,000 characters**, roughly 10,000 tokens, through `max_state_chars`. Five questions then fit well inside the 32,000-token per-question limit, and padding the state would cost accuracy as well as money.

**The payload pins the model** to `jev-1.13.0`. The SDK defaults to `jev-latest`, so a silent upgrade would move any threshold you tuned against measured behavior.

**Confidence and the winning probability are separate fields.** That run put 0.99 on `user` and reported confidence 0.98. Gate on `.confidence` when you care how sure Jev is of the label, and read `.probabilities` when you care how close the runner-up came.

**A `Score` lands between levels, and its `legend` names them.** Jev returns the rubric text keyed by level, 0 upward in the order you wrote the criteria. The score is the probability-weighted average of those levels, so the sample prints the two it sits between rather than making the reader count.

**A `Noul` has no separate confidence.** The probability is the confidence, so the sample turns it into a word through the `NOUL_WORDS` bands: 0.99 reads as yes, 0.85 as probably yes, 0.29 as probably no. A value near 0.50 says the document argues both ways or never addresses the statement.

**The same document scores a little differently on each call.** Six calls against `psf/requests` on 19 September 2026 put `sounds stale` between 0.29 and 0.31, which straddles the 0.30 band edge and flips the printed word between "probably no" and "unsettled". Sampling noise of a couple of hundredths will cross any threshold you park on a round number, so measure the spread before you pick one.

## Two experiments worth running

Point it at twenty READMEs you already know well and read the output against what is in the files. You are testing calibration on your own material: when Jev says 0.9, it should be right about nine times in ten.

1. Add a sixth question to `questions.yml` and time it. The state is the cost, so the wall clock holds about steady.
2. Change `model` in `questions.yml` to `jev-latest`, re-run the same files, and see what the pin was protecting.
