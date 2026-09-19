"""Ask Jev five questions about a README in a single call.

Reads a README from disk or from a GitHub URL, sends it to Jev as state, and
asks five questions at once: one Choice, one Score and three Nouls. Nothing is
parsed on the way back, because Jev never returns prose.

Usage:
    uv run readme_check.py                                   # ./README.md
    uv run readme_check.py path/to/README.md                 # any local file
    uv run readme_check.py https://github.com/psf/requests   # a GitHub repo
"""

import argparse
import logging
import pathlib
import sys
import urllib.request

from typesafe_sdk import (
    Choice,
    Noul,
    Score,
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


def _to_raw_url(
    url: str
) -> str:
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
        return url.replace("github.com", "raw.githubusercontent.com", 1).replace(
            "/blob/", "/", 1
        )

    parts = url.rstrip("/").split("/")
    if len(parts) == GITHUB_REPO_URL_PART_COUNT:  # a repo root
        owner, repo = parts[3], parts[4]
        return f"https://raw.githubusercontent.com/{owner}/{repo}/HEAD/README.md"

    return url


def _load_readme(
    target: str
) -> tuple[str, str]:
    """Load a README from a local path or an https URL.

    Args:
        target: A filesystem path or an https URL.

    Returns:
        Tuple of (display name, document text).

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
        return path.name, path.read_text(encoding="utf-8")

    url = _to_raw_url(target)
    logger.info(f"Fetching: {url}")
    with urllib.request.urlopen(url, timeout=FETCH_TIMEOUT_SECONDS) as response:  # nosec B310 - https-only, enforced above
        text = response.read().decode("utf-8", "replace")
    return url.rsplit("/", 1)[-1], text


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


def _print_answers(
    name: str,
    answers: dict
) -> None:
    """Print the answers, then the one verdict worth acting on.

    Args:
        name: Display name of the document that was read.
        answers: Answers keyed by question id, as returned by Jev.
    """
    audience = answers["audience"]
    print(name)
    print(f"  written for     {audience.choice:12} ({audience.confidence:.2f})")
    print(f"  setup steps     {answers['setup'].score:.1f} / 2")
    print(f"  worked example  {answers['has_example'].noul:.2f}")
    print(f"  auth explained  {answers['explains_auth'].noul:.2f}")
    print(f"  sounds stale    {answers['sounds_stale'].noul:.2f}")

    # Two gates, each written next to the thing it guards.
    if answers["setup"].score < 1 or answers["has_example"].noul < 0.3:
        print("\n  -> worth a rewrite before anyone outside the team reads it")


def check_readme(
    target: str
) -> None:
    """Read one README and ask Jev five questions about it in one call.

    Args:
        target: A filesystem path or an https URL.
    """
    name, text = _load_readme(target)

    answers = TypeSafeClient().system_one(
        model=MODEL,
        state={"filename": name, "readme": text[:MAX_STATE_CHARS]},
        questions=_build_questions(),
    ).answers

    _print_answers(name, answers)


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
    args = parser.parse_args()

    if args.debug:
        logging.getLogger().setLevel(logging.DEBUG)

    check_readme(args.target)


if __name__ == "__main__":
    main()
