# jev-samples

Runnable samples for [Jev](https://typesafe.ai/blog/introducing-system-one-models-and-jev), TypeSafe AI's System One model: unstructured state in, typed decisions out. You hand it state and a list of questions, each carrying its possible answers written out in advance, and you get the answers back with no prose wrapped around them. Nothing to parse, nothing to retry.

Every sample is a small self-contained project under [samples/](samples/). Each one is real code you can run against your own files, not a snippet.

## Samples

| Sample | What it does |
| --- | --- |
| [readme-check](samples/readme-check/) | Reads a README from disk or a GitHub URL and asks five questions about it in one call, using all three primitives: `Choice`, `Score` and `Noul`. |

More to come. One folder per sample.

## Prerequisites

- Python 3.11 or newer
- [uv](https://docs.astral.sh/uv/) for dependencies and running
- A TypeSafe API key in `TYPESAFE_API_KEY`

```bash
cp .env.example .env    # then put your key in it
export TYPESAFE_API_KEY="your-key"
```

## Running a sample

Each sample folder is its own uv project, so `uv run` resolves its dependencies on first use.

```bash
cd samples/readme-check
uv run readme_check.py --help
```

## The three primitives

| Type | Asks | Returns |
| --- | --- | --- |
| `Noul` | Is this statement true of the state? | `.noul`, a probability from 0 to 1. No separate confidence field, because the number is the confidence. |
| `Choice` | Which of these options fits? | `.choice`, `.probabilities` over every option, `.confidence`. |
| `Score` | Where on this ordered rubric does it sit? | `.score`, which lands between levels, `.probabilities`, `.confidence`. |

Two rules govern how you write questions. Keep each one atomic, asking about exactly one thing, because "is this ticket urgent and about billing?" has no honest answer when the ticket is urgent and about something else. And when a judgment has several parts, keep the weighting in your own code, where you can read it in a diff.

## Background

These samples come out of an explainer on how Jev works, where it breaks, and what to measure before you trust a confidence threshold: [Jev: a model that decides instead of writing](https://github.com/aarora79/my-ai-assets/blob/main/explainers/jev/jev-explainer.md).

Worth reading before you build a gate on top of Jev:

- [TypeSafe documentation](https://docs.typesafe.ai/) for the primitives, state shape and HTTP API
- [typesafe-sdk-python](https://github.com/typesafe-ai/typesafe-sdk-python), the official Python SDK
- [openjev-sglang](https://github.com/ekzhang/openjev-sglang), an open reproduction of the call shape on weights you can host
- [jevcal](https://github.com/abhixhek/jevcal), which turns a confidence threshold from a guess into a measurement

Accuracy is 67.8% on TypeSafe's own benchmark, and nobody outside the company has reproduced the latency yet. Measure both on your own data before a threshold guards anything that matters.
