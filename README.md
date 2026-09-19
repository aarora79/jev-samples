# jev-samples

Runnable samples for [Jev](https://typesafe.ai/blog/introducing-system-one-models-and-jev), TypeSafe AI's System One model. You send it state and a list of questions, each question carrying its possible answers. It sends back the answers with no prose around them, so your code branches on a value instead of parsing one out of a paragraph.

Every sample is a small self-contained project under [samples/](samples/), and you can run each one against your own files.

## Samples

| Sample | What it does |
| --- | --- |
| [readme-check](samples/readme-check/) | Reads a README from disk or a GitHub URL and asks five questions about it in one call, using all three primitives: `Choice`, `Score` and `Noul`. |

More samples to come, one folder each.

## Prerequisites

- Python 3.11 or newer
- [uv](https://docs.astral.sh/uv/) for dependencies and running
- A TypeSafe API key in `TYPESAFE_API_KEY`

Nothing here auto-loads `.env`, which would cost a dependency for one line of shell. The SDK reads `TYPESAFE_API_KEY` from the environment:

```bash
cp .env.example .env         # put your key in it
set -a && . ./.env && set +a # load it into the environment

# or skip the file
export TYPESAFE_API_KEY="your-key"
```

## Try it with curl

One endpoint does everything: `POST https://api.typesafe.ai/v1/systemone`. The body holds `model`, `state` and `questions`. This call asks all three question types at once about one support ticket, which is every option the model takes.

```bash
export TYPESAFE_API_KEY="your-key"

curl -s https://api.typesafe.ai/v1/systemone \
  -H "Authorization: Bearer $TYPESAFE_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "jev-1.13.0",
    "state": {
      "ticket": "I have been charged twice for order A-104. The second charge hit this morning. I have emailed twice and nobody has replied. Please refund the duplicate today."
    },
    "questions": {
      "team": {
        "type": "choice",
        "instructions": "Which team should handle this ticket",
        "criteria": {
          "billing": "Charges, invoices, refunds",
          "technical": "Something is broken or behaving wrong",
          "sales": "Pricing, plans, account changes"
        }
      },
      "anger": {
        "type": "score",
        "instructions": "How angry the customer sounds",
        "criteria": [
          "Calm, stating facts",
          "Frustrated but civil",
          "Furious, threatening to leave"
        ]
      },
      "wants_refund": {
        "type": "noul",
        "instructions": "The customer is asking for money back"
      }
    }
  }' | python3 -m json.tool
```

The response, from a run on 19 September 2026:

```json
{
    "model": "jev-1.13.0",
    "answers": {
        "team": {
            "type": "choice",
            "choice": "billing",
            "confidence": 1.0,
            "probabilities": {
                "technical": 0.0,
                "sales": 0.0,
                "billing": 1.0
            }
        },
        "anger": {
            "type": "score",
            "score": 1.0,
            "confidence": 1.0,
            "legend": {
                "0": "Calm, stating facts",
                "1": "Frustrated but civil",
                "2": "Furious, threatening to leave"
            },
            "probabilities": {
                "0": 0.0,
                "1": 1.0,
                "2": 0.0
            }
        },
        "wants_refund": {
            "type": "noul",
            "noul": 0.99
        }
    },
    "usage": {
        "input_tokens": 439,
        "output_tokens": 71
    }
}
```

Three things to read off that response. A `choice` answer names one of your options and shows where the rest of the probability mass went, and this ticket left none of it elsewhere. A `score` answer carries a `legend` mapping each rubric level back to the text you wrote, so 1.0 means "frustrated but civil". A `noul` answer is one number, and 0.99 is both the answer and the confidence.

TypeSafe prices input tokens only, at $0.042 per million, so these 439 input tokens cost about $0.000018. The API still counts output tokens, and reported 71 here.

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
| `Score` | Where on this ordered rubric does it sit? | `.score`, which lands between levels, plus `.legend` mapping each level back to your text, `.probabilities`, `.confidence`. |

Keep each question atomic, asking about one thing. "Is this ticket urgent and about billing?" has no honest answer when the ticket is urgent and about something else, and the model answers half your question without telling you which half. When a judgment has several parts, weight them in your own code, where you can read the weights in a diff.

## Background

These samples come out of an explainer on how Jev works and what to measure before you trust a confidence threshold: [Jev: a model that decides instead of writing](https://github.com/aarora79/my-ai-assets/blob/main/explainers/jev/jev-explainer.md).

Worth reading before you build a gate on top of Jev:

- [TypeSafe documentation](https://docs.typesafe.ai/) for the primitives, state shape and HTTP API
- [typesafe-sdk-python](https://github.com/typesafe-ai/typesafe-sdk-python), the official Python SDK
- [openjev-sglang](https://github.com/ekzhang/openjev-sglang), an open reproduction of the call shape on weights you can host
- [jevcal](https://github.com/abhixhek/jevcal), which turns a confidence threshold from a guess into a measurement

Accuracy is 67.8% on TypeSafe's own benchmark, and nobody outside the company has reproduced the latency yet. Measure both on your own data before a threshold guards anything that matters.
