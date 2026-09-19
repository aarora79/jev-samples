"""Ask Jev five questions about a README in a single call.

Reads a README from disk or from a GitHub URL, sends it to Jev as state, and
asks five questions at once: one Choice, one Score and three Nouls. Nothing is
parsed on the way back, because Jev never returns prose.

Usage:
    uv run readme_check.py                                   # ./README.md
    uv run readme_check.py path/to/README.md                 # any local file
    uv run readme_check.py https://github.com/psf/requests   # a GitHub repo
    uv run readme_check.py --verbose                         # raw answers too
"""

import argparse
import json
import logging
import pathlib
import sys
import urllib.request

from typesafe_sdk import (
    Choice,
    ChoiceAnswer,
    Noul,
    Score,
    ScoreAnswer,
    SystemOneResponse,
    TypeSafeClient,
)

# Configure logging with basicConfig
logging.basicConfig(
    level=logging.INFO,  # Set the log level to INFO
    # Define log message format
    format="%(asctime)s,p%(process)s,{%(filename)s:%(lineno)d},%(levelname)s,%(message)s",
)
logger = logging.getLogger(__name__)

# Pin the model. Both SDKs default to jev-latest, and a silent upgrade would
# move answers under thresholds calibrated against an older version.
MODEL: str = "jev-1.13.0"

# Roughly 10,000 tokens. Keeps five questions well inside the 32,000-token
# per-question limit, and stops a long document from costing accuracy.
MAX_STATE_CHARS: int = 40_000

FETCH_TIMEOUT_SECONDS: int = 20

DEFAULT_TARGET: str = "README.md"

GITHUB_REPO_URL_PART_COUNT: int = 5

# The three Noul question ids, mapped to their display labels in print order.
NOUL_LABELS: dict[str, str] = {
    "has_example": "worked example",
    "explains_auth": "auth explained",
    "sounds_stale": "sounds stale",
}

# A word for each band of a Noul probability, highest floor first. Jev returns
# one number and no separate confidence, so 0.5 says the document never settled
# the question either way.
NOUL_WORDS: tuple[tuple[float, str], ...] = (
    (0.9, "yes"),
    (0.7, "probably yes"),
    (0.3, "unsettled"),
    (0.1, "probably no"),
    (0.0, "no"),
)


def _to_raw_url(url: str) -> str:
    """Rewrite a GitHub web URL to its raw.githubusercontent.com equivalent.

    Args:
        url: An https URL, GitHub or otherwise.

    Returns:
        The raw URL for GitHub file pages and repo roots, the input unchanged
        for anything else.
    """
    if "github.com" not in url:
        return url

    if "/blob/" in url:  # a file page
        return url.replace("github.com", "raw.githubusercontent.com", 1).replace("/blob/", "/", 1)

    parts = url.rstrip("/").split("/")
    if len(parts) == GITHUB_REPO_URL_PART_COUNT:  # a repo root
        owner, repo = parts[3], parts[4]
        return f"https://raw.githubusercontent.com/{owner}/{repo}/HEAD/README.md"

    return url


def _load_readme(target: str) -> tuple[str, str, str]:
    """Load a README from a local path or an https URL.

    Args:
        target: A filesystem path or an https URL.

    Returns:
        Tuple of (display name, the path or URL it read, document text). The
        source is resolved, so a GitHub repo root comes back as the raw URL the
        fetch used and a relative path comes back absolute.

    Raises:
        SystemExit: If the target is plain http, or the local file is missing.
    """
    if target.startswith("http://"):
        sys.exit("https only: point it at the https:// URL instead")

    if not target.startswith("https://"):
        path = pathlib.Path(target)
        if not path.exists():
            sys.exit(f"no file at {path}")
        logger.info(f"Reading local file: {path}")
        return path.name, str(path.resolve()), path.read_text(encoding="utf-8")

    url = _to_raw_url(target)
    logger.info(f"Fetching: {url}")
    with urllib.request.urlopen(url, timeout=FETCH_TIMEOUT_SECONDS) as response:  # nosec B310 - https-only, enforced above
        text = response.read().decode("utf-8", "replace")
    return url.rsplit("/", 1)[-1], url, text


def _build_questions() -> dict:
    """Build the five questions, one per thing worth knowing about a README.

    Each question is atomic: it asks about exactly one thing, so no answer has
    to cover two claims at once.

    Returns:
        Question ids mapped to Choice, Score and Noul instances.
    """
    return {
        "audience": Choice(
            instructions="Who is this README written for",
            criteria={
                "user": "Someone who wants to install and use the thing",
                "contributor": "Someone who wants to change the code",
                "evaluator": "Someone deciding whether to adopt it",
            },
        ),
        "setup": Score(
            instructions="How complete the setup instructions are",
            criteria=[
                "No install or setup steps at all",
                "Steps exist but assume things they never state",
                "A reader could follow them start to finish",
            ],
        ),
        "has_example": Noul(
            instructions="The document shows at least one worked example",
        ),
        "explains_auth": Noul(
            instructions="The document explains how to authenticate",
        ),
        "sounds_stale": Noul(
            instructions="The document mentions versions or features that sound out of date",
        ),
    }


def _describe_noul(value: float) -> str:
    """Turn one Noul probability into the word it stands for.

    Args:
        value: The probability Jev returned for the statement, from 0 to 1.

    Returns:
        The word for the band the probability falls in.
    """
    for floor, wording in NOUL_WORDS:
        if value >= floor:
            return wording
    return NOUL_WORDS[-1][1]


def _describe_choice(answer: ChoiceAnswer) -> str:
    """List every option Jev scored and the probability it gave each one.

    Args:
        answer: The Choice answer, carrying choice, confidence and
            probabilities.

    Returns:
        One line of "label 0.99" pairs, best first.
    """
    ranked = sorted(answer.probabilities.items(), key=lambda item: item[1], reverse=True)
    return ", ".join(f"{label} {probability:.2f}" for label, probability in ranked)


def _describe_score(answer: ScoreAnswer) -> list[str]:
    """Read a Score against the legend Jev returns beside it.

    The score is the probability-weighted average of the rubric levels, so it
    lands between two of them and the legend names both.

    Args:
        answer: The Score answer, carrying score, confidence, legend and
            probabilities.

    Returns:
        Explanation lines, one per printed line.
    """
    ranked = sorted(answer.probabilities.items())
    levels = ", ".join(f"{level} {value:.2f}" for level, value in ranked)
    # The SDK keys legend and probabilities by integer level, counting from 0
    # in the order the criteria were written.
    lower = min(int(answer.score), len(answer.legend) - 1)
    upper = min(lower + 1, len(answer.legend) - 1)
    if lower == upper:
        return [
            f'Score: level {lower}, "{answer.legend[lower]}".',
            f"Weighted from the level probabilities {levels}.",
        ]
    return [
        f'Score: between level {lower} "{answer.legend[lower]}"',
        f'and level {upper} "{answer.legend[upper]}".',
        f"Jev weights the levels by probability ({levels}), so the score lands between two of them.",
    ]


def _print_raw(response: SystemOneResponse) -> None:
    """Pretty print everything Jev returned, before the sample reads any of it.

    Args:
        response: The response object the SDK built from the wire body.
    """
    print("\nRaw response:")
    print(json.dumps(response.model_dump(mode="json"), indent=2))


def _print_answers(
    name: str,
    source: str,
    answers: dict,
) -> None:
    """Print each answer, what the number means, then the one verdict.

    Every explanation comes out of the answer itself: the probabilities Jev
    spread across the options, the legend it returns with a Score, and the
    confidence it reports for both.

    Args:
        name: Display name of the document that was read.
        source: The path or URL the text came from.
        answers: Answers keyed by question id, as returned by Jev.
    """
    audience = answers["audience"]
    setup = answers["setup"]
    print(f"\n{name}  ({source})\n")

    print(f"  written for     {audience.choice:12} (confidence {audience.confidence:.2f})")
    options = len(audience.probabilities)
    print(f"      Choice: one label out of {options}, scored {_describe_choice(audience)}.")
    print(f"      Confidence {audience.confidence:.2f} rates that pick, and Jev reports it")
    print("      apart from the spread, so the two numbers can differ.\n")

    top = len(setup.legend) - 1
    print(f"  setup steps     {setup.score:.1f} / {top}      (confidence {setup.confidence:.2f})")
    for line in _describe_score(setup):
        print(f"      {line}")
    print()

    for question_id, label in NOUL_LABELS.items():
        value = answers[question_id].noul
        print(f"  {label:15} {value:.2f}         {_describe_noul(value)}")
    print("      Noul: one probability, which is also the confidence. Near 0.50 says the")
    print("      document argues both ways, or never addresses the statement at all.")

    # Two gates, each written next to the thing it guards.
    if setup.score < 1 or answers["has_example"].noul < 0.3:
        print("\n  -> worth a rewrite before anyone outside the team reads it")


def check_readme(
    target: str,
    verbose: bool = False,
) -> None:
    """Read one README and ask Jev five questions about it in one call.

    Args:
        target: A filesystem path or an https URL.
        verbose: Print the raw answers before the explained summary.
    """
    name, source, text = _load_readme(target)

    response = TypeSafeClient().system_one(
        model=MODEL,
        state={"filename": name, "readme": text[:MAX_STATE_CHARS]},
        questions=_build_questions(),
    )
    logger.debug(f"Used {response.usage.input_tokens} input tokens")

    if verbose:
        _print_raw(response)

    _print_answers(name, source, response.answers)


def main() -> None:
    """Parse arguments and run the check."""
    parser = argparse.ArgumentParser(
        description="Ask Jev five questions about a README in a single call.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Example usage:
    # The README in the current directory
    uv run readme_check.py

    # Any local file
    uv run readme_check.py path/to/README.md

    # A GitHub repo root or file page
    uv run readme_check.py https://github.com/psf/requests

    # Print the raw answers Jev returned, then the explained summary
    uv run readme_check.py --verbose https://github.com/psf/requests

Requires TYPESAFE_API_KEY in the environment.
""",
    )
    parser.add_argument(
        "target",
        nargs="?",
        default=DEFAULT_TARGET,
        help=f"Local path or https URL to read (default: {DEFAULT_TARGET})",
    )
    parser.add_argument(
        "--debug",
        action="store_true",
        help="Turn on debug logging",
    )
    parser.add_argument(
        "--verbose",
        action="store_true",
        help="Pretty print the raw JSON Jev returned, then the explained summary",
    )
    args = parser.parse_args()

    if args.debug:
        logging.getLogger().setLevel(logging.DEBUG)

    check_readme(args.target, args.verbose)


if __name__ == "__main__":
    main()
