"""Ask Jev five questions about a README in a single call.

Reads a README from disk or from a GitHub URL, sends it to Jev as state, and asks
every question in questions.yml at once: one Choice, one Score and three Nouls.
Nothing is parsed on the way back, because Jev never returns prose.

The payload lives in questions.yml. The judgment lives here, where each threshold
sits beside the action it guards.

Usage:
    uv run readme_check.py                                   # ./README.md
    uv run readme_check.py path/to/README.md                 # any local file
    uv run readme_check.py https://github.com/psf/requests   # a GitHub repo
    uv run readme_check.py --verbose                         # raw answers too
"""

import argparse
import json
import logging
import os
import pathlib
import sys
import urllib.request

import yaml
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

# The model pin, the state budget and every question. Sits beside this file so
# `uv run readme_check.py` finds it from any working directory.
PAYLOAD_FILE: pathlib.Path = pathlib.Path(__file__).parent / "questions.yml"

FETCH_TIMEOUT_SECONDS: int = 20

# The SDK reads the key from this variable.
API_KEY_ENV: str = "TYPESAFE_API_KEY"

# Where to look when the environment has no key: beside the sample, then at the
# repo root. The sample reads one variable out of the file and logs the path it
# came from, never the value.
ENV_FILES: tuple[pathlib.Path, ...] = (
    pathlib.Path(__file__).parent / ".env",
    pathlib.Path(__file__).parents[2] / ".env",
)

DEFAULT_TARGET: str = "README.md"

GITHUB_REPO_URL_PART_COUNT: int = 5

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


def _build_question(
    question_id: str,
    spec: dict,
) -> Choice | Score | Noul:
    """Turn one questions.yml entry into the SDK object for its type.

    Args:
        question_id: The key the entry sits under, used in error messages.
        spec: The entry itself, holding type, instructions and any criteria.

    Returns:
        A Choice, Score or Noul built from the entry.

    Raises:
        SystemExit: If the entry names a type Jev does not have.
    """
    kind = spec.get("type")
    if kind == "noul":
        return Noul(instructions=spec["instructions"])
    if kind == "choice":
        return Choice(instructions=spec["instructions"], criteria=spec["criteria"])
    if kind == "score":
        return Score(instructions=spec["instructions"], criteria=spec["criteria"])
    sys.exit(f"{PAYLOAD_FILE.name}: question {question_id} has unknown type {kind!r}")


def _load_payload() -> tuple[dict, dict]:
    """Read the model pin, the state budget and the questions from questions.yml.

    Returns:
        Tuple of (settings, specs). Settings holds the model and the state
        budget; specs holds each question entry keyed by id, in file order.

    Raises:
        SystemExit: If the file is missing a key the request needs.
    """
    payload = yaml.safe_load(PAYLOAD_FILE.read_text(encoding="utf-8"))
    missing = {"model", "max_state_chars", "questions"} - payload.keys()
    if missing:
        sys.exit(f"{PAYLOAD_FILE.name}: missing {', '.join(sorted(missing))}")

    settings = {"model": payload["model"], "max_state_chars": payload["max_state_chars"]}
    return settings, payload["questions"]


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
        return f"https://raw.githubusercontent.com/{owner}/{repo}/HEAD/{DEFAULT_TARGET}"

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
    specs: dict,
) -> None:
    """Print each answer, what the number means, then the one verdict.

    Every explanation comes out of the answer itself: the probabilities Jev
    spread across the options, the legend it returns with a Score, and the
    confidence it reports for both.

    Args:
        name: Display name of the document that was read.
        source: The path or URL the text came from.
        answers: Answers keyed by question id, as returned by Jev.
        specs: Question entries from questions.yml, keyed by id and in file
            order, which is the order this prints them.
    """
    audience = answers["audience"]
    setup = answers["setup"]
    print(f"\n{name}  ({source})\n")

    print(
        f"  {specs['audience']['label']:15} {audience.choice:12} (confidence {audience.confidence:.2f})"
    )
    print(
        f"      Choice: one label out of {len(audience.probabilities)}, scored {_describe_choice(audience)}."
    )
    print(f"      Confidence {audience.confidence:.2f} rates that pick, and Jev reports it")
    print("      apart from the spread, so the two numbers can differ.\n")

    top = len(setup.legend) - 1
    print(
        f"  {specs['setup']['label']:15} {setup.score:.1f} / {top}      (confidence {setup.confidence:.2f})"
    )
    for line in _describe_score(setup):
        print(f"      {line}")
    print()

    for question_id, spec in specs.items():
        if spec["type"] != "noul":
            continue
        value = answers[question_id].noul
        print(f"  {spec['label']:15} {value:.2f}         {_describe_noul(value)}")
    print("      Noul: one probability, which is also the confidence. Near 0.50 says the")
    print("      document argues both ways, or never addresses the statement at all.")

    # Two gates, each written next to the thing it guards.
    if setup.score < 1 or answers["has_example"].noul < 0.3:
        print("\n  -> worth a rewrite before anyone outside the team reads it")


def _key_from_env_files() -> str | None:
    """Read TYPESAFE_API_KEY out of the first .env file that sets it.

    Handles the two shapes a key file takes in practice: `NAME=value` and
    `export NAME=value`, with or without quotes.

    Returns:
        The key, or None when no file sets it.
    """
    for path in ENV_FILES:
        if not path.exists():
            continue
        for line in path.read_text(encoding="utf-8").splitlines():
            name, _, value = line.strip().removeprefix("export ").partition("=")
            if name.strip() != API_KEY_ENV:
                continue
            logger.info(f"Read {API_KEY_ENV} from {path}")
            return value.strip().strip("\"'")
    return None


def _require_api_key() -> None:
    """Put the key in the environment before anything reaches the network.

    The environment wins. A .env file beside the sample or at the repo root is
    the fallback, so an activated virtualenv with no exports still runs.

    Raises:
        SystemExit: If neither the environment nor a .env file has the key.
    """
    if os.environ.get(API_KEY_ENV):
        return

    key = _key_from_env_files()
    if not key:
        looked = ", ".join(str(path) for path in ENV_FILES)
        sys.exit(
            f"{API_KEY_ENV} is not set, and no key sits in {looked}.\n"
            f"  export {API_KEY_ENV}=your-key"
        )
    os.environ[API_KEY_ENV] = key


def check_readme(
    target: str,
    verbose: bool = False,
) -> None:
    """Read one README and ask Jev every question in the payload, in one call.

    Args:
        target: A filesystem path or an https URL.
        verbose: Print the raw answers before the explained summary.
    """
    _require_api_key()
    settings, specs = _load_payload()
    questions = {
        question_id: _build_question(question_id, spec) for question_id, spec in specs.items()
    }
    name, source, text = _load_readme(target)

    response = TypeSafeClient().system_one(
        model=settings["model"],
        state={"filename": name, "readme": text[: settings["max_state_chars"]]},
        questions=questions,
    )
    logger.debug(f"Used {response.usage.input_tokens} input tokens")

    if verbose:
        _print_raw(response)

    _print_answers(name, source, response.answers, specs)


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

The questions, the model pin and the state budget live in questions.yml.
Reads TYPESAFE_API_KEY from the environment, or from .env beside this file or at
the repo root.
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
