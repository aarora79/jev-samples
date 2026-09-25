"""Build a triage dataset from one repository's pull requests.

Reads the GitHub REST API and writes one JSON file holding, per pull request,
the title, the description, the base branch, the per-file stats and the patch
for every changed file. That file is the dataset pr_triage.py sends to Jev, and
committing it makes a triage run reproducible without hitting GitHub again.

Needs no Jev key. Reads GITHUB_TOKEN or GH_TOKEN, and falls back to
`gh auth token` when neither is set. Unauthenticated calls get 60 an hour, and
one pull request costs two of them, so a token is worth having.

Usage:
    uv run fetch_prs.py owner/repo                      # 10 most recent open PRs
    uv run fetch_prs.py owner/repo --limit 25           # the 25 most recent
    uv run fetch_prs.py owner/repo --since 2026-08-01   # opened on or after a date
    uv run fetch_prs.py owner/repo --all                # every open PR
"""

import argparse
import datetime
import json
import logging
import os
import pathlib
import re
import subprocess  # nosec B404 - reads a token from the gh CLI, list form only
import sys
import urllib.error
import urllib.parse
import urllib.request

# Configure logging with basicConfig
logging.basicConfig(
    level=logging.INFO,  # Set the log level to INFO
    # Define log message format
    format="%(asctime)s,p%(process)s,{%(filename)s:%(lineno)d},%(levelname)s,%(message)s",
)
logger = logging.getLogger(__name__)

API_ROOT: str = "https://api.github.com"

# The version header GitHub asks callers to pin. Without it the API is free to
# change shape under the sample.
API_VERSION: str = "2022-11-28"

FETCH_TIMEOUT_SECONDS: int = 30

# GitHub's maximum, and the fewest round trips per repository.
PER_PAGE: int = 100

# Stop paginating here rather than walking a repository with 4,000 open pull
# requests. --all means all of them up to this.
MAX_PAGES: int = 20

# Where the environment carries a token, in the order checked.
TOKEN_ENV_NAMES: tuple[str, ...] = ("GITHUB_TOKEN", "GH_TOKEN")

GH_TOKEN_TIMEOUT_SECONDS: int = 10

# One generated lockfile diff runs to hundreds of thousands of characters and
# would dominate the dataset on disk. Cutting each patch here keeps a dataset
# readable, and pr_triage.py applies its own, smaller budget when it builds the
# state it sends.
MAX_PATCH_CHARS: int = 40000

# How many recent pull requests to take when the caller names no selector.
DEFAULT_LIMIT: int = 10

DATASET_DIR: pathlib.Path = pathlib.Path(__file__).parent / "data"

# `owner/repo`, which is what the API path wants and what `gh` prints.
REPO_PATTERN: re.Pattern[str] = re.compile(r"^[\w.-]+/[\w.-]+$")


def _token_from_gh_cli() -> str | None:
    """Ask the gh CLI for the token it already holds.

    Returns:
        The token, or None when gh is missing or logged out.
    """
    try:
        result = subprocess.run(  # nosec B603 B607 - hardcoded command, no user input
            ["gh", "auth", "token"],
            capture_output=True,
            text=True,
            timeout=GH_TOKEN_TIMEOUT_SECONDS,
            check=True,
        )
    except FileNotFoundError:
        logger.info("No gh CLI on PATH, so calling GitHub unauthenticated")
        return None
    except subprocess.TimeoutExpired:
        logger.warning("gh auth token timed out, so calling GitHub unauthenticated")
        return None
    except subprocess.CalledProcessError:
        logger.info("gh CLI is not logged in, so calling GitHub unauthenticated")
        return None

    logger.info("Read a GitHub token from the gh CLI")
    return result.stdout.strip() or None


def _token() -> str | None:
    """Find a GitHub token, preferring the environment over the gh CLI.

    Returns:
        The token, or None to call the API unauthenticated.
    """
    for name in TOKEN_ENV_NAMES:
        value = os.environ.get(name)
        if value:
            logger.info(f"Read a GitHub token from {name}")
            return value
    return _token_from_gh_cli()


def _get_json(
    url: str,
    token: str | None,
) -> list | dict:
    """Fetch one API URL and parse the body as JSON.

    Args:
        url: A full https URL under API_ROOT.
        token: Bearer token, or None for an unauthenticated call.

    Returns:
        The parsed body.

    Raises:
        SystemExit: On a 401, 403 or 404, each of which has one likely cause
            worth naming.
    """
    headers = {
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": API_VERSION,
        "User-Agent": "jev-samples-pr-triage",
    }
    if token:
        headers["Authorization"] = f"Bearer {token}"

    request = urllib.request.Request(url, headers=headers)  # nosec B310 - https-only, built from API_ROOT
    try:
        with urllib.request.urlopen(request, timeout=FETCH_TIMEOUT_SECONDS) as response:  # nosec B310 - https-only, built from API_ROOT
            return json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as error:
        if error.code == 404:
            sys.exit(
                f"GitHub returned 404 for {url}\n  check the repo name, and that you can see it"
            )
        if error.code in (401, 403):
            remaining = error.headers.get("x-ratelimit-remaining")
            if remaining == "0":
                sys.exit(
                    "GitHub rate limit exhausted. Set GITHUB_TOKEN, or wait for the window to reset."
                )
            sys.exit(
                f"GitHub returned {error.code} for {url}\n  the token is missing, expired, or lacks access"
            )
        raise


def _matches_since(
    pull: dict,
    since: datetime.date | None,
) -> bool:
    """Say whether a pull request was opened on or after a date.

    Args:
        pull: One entry from the pulls list endpoint.
        since: The cutoff, or None to accept everything.

    Returns:
        True when the pull request is in range.
    """
    if since is None:
        return True
    created = datetime.datetime.fromisoformat(pull["created_at"]).date()
    return created >= since


def _list_pulls(
    repo: str,
    token: str | None,
    state: str,
    limit: int | None,
    since: datetime.date | None,
) -> list[dict]:
    """List pull requests newest first, stopping as soon as the selector is met.

    The pulls endpoint takes no date filter, so the date cut happens here. Newest
    first means the first pull request older than the cutoff ends the walk.

    Args:
        repo: The repository as `owner/repo`.
        token: Bearer token, or None.
        state: open, closed or all.
        limit: How many to keep, or None for every one the selector allows.
        since: Keep only pull requests opened on or after this date.

    Returns:
        The list entries, newest first.
    """
    kept: list[dict] = []
    for page in range(1, MAX_PAGES + 1):
        query = urllib.parse.urlencode(
            {
                "state": state,
                "sort": "created",
                "direction": "desc",
                "per_page": PER_PAGE,
                "page": page,
            }
        )
        logger.info(f"Listing {state} pull requests for {repo}, page {page}")
        batch = _get_json(f"{API_ROOT}/repos/{repo}/pulls?{query}", token)

        for pull in batch:
            if not _matches_since(pull, since):
                logger.info(f"Reached #{pull['number']}, opened before the cutoff, so stopping")
                return kept
            kept.append(pull)
            if limit is not None and len(kept) >= limit:
                return kept

        if len(batch) < PER_PAGE:  # the last page
            return kept

    logger.warning(f"Stopped at {MAX_PAGES} pages, so the dataset may be short")
    return kept


def _pull_files(
    repo: str,
    number: int,
    token: str | None,
) -> tuple[list[dict], bool]:
    """Fetch the changed files for one pull request, with their patches.

    Args:
        repo: The repository as `owner/repo`.
        number: The pull request number.
        token: Bearer token, or None.

    Returns:
        Tuple of (files, truncated). Each file carries its path, status, line
        counts and patch. Truncated says whether MAX_PAGES cut the list short,
        which GitHub also does at 3,000 files.
    """
    files: list[dict] = []
    for page in range(1, MAX_PAGES + 1):
        query = urllib.parse.urlencode({"per_page": PER_PAGE, "page": page})
        batch = _get_json(f"{API_ROOT}/repos/{repo}/pulls/{number}/files?{query}", token)

        for entry in batch:
            # A binary file, or one GitHub declined to diff, carries no patch.
            patch = entry.get("patch") or ""
            files.append(
                {
                    "path": entry["filename"],
                    "previous_path": entry.get("previous_filename"),
                    "status": entry["status"],
                    "additions": entry["additions"],
                    "deletions": entry["deletions"],
                    "patch": patch[:MAX_PATCH_CHARS],
                    "patch_truncated": len(patch) > MAX_PATCH_CHARS,
                    "has_patch": bool(patch),
                }
            )

        if len(batch) < PER_PAGE:
            return files, False

    return files, True


def _pull_record(
    repo: str,
    entry: dict,
    token: str | None,
) -> dict:
    """Turn one list entry into the dataset record, fetching what it lacks.

    The list endpoint carries no line counts, so this reads the pull request
    itself for `changed_files`, `additions` and `deletions`, then its files.

    Args:
        repo: The repository as `owner/repo`.
        entry: One entry from the pulls list endpoint.
        token: Bearer token, or None.

    Returns:
        One dataset record.
    """
    number = entry["number"]
    logger.info(f"Fetching #{number}: {entry['title'][:60]}")
    detail = _get_json(f"{API_ROOT}/repos/{repo}/pulls/{number}", token)
    files, files_truncated = _pull_files(repo, number, token)

    return {
        "number": number,
        "title": entry["title"],
        "body": entry.get("body") or "",
        "author": (entry.get("user") or {}).get("login", ""),
        "created_at": entry["created_at"],
        "updated_at": entry["updated_at"],
        "draft": bool(entry.get("draft")),
        "labels": [label["name"] for label in entry.get("labels", [])],
        "base": entry["base"]["ref"],
        "url": entry["html_url"],
        "changed_files": detail["changed_files"],
        "additions": detail["additions"],
        "deletions": detail["deletions"],
        "files": files,
        "files_truncated": files_truncated,
    }


def _selector_slug(
    state: str,
    limit: int | None,
    since: datetime.date | None,
) -> str:
    """Name the selector, so two datasets for one repo sit in separate files.

    Args:
        state: open, closed or all.
        limit: How many were kept, or None.
        since: The date cutoff, or None.

    Returns:
        A filename fragment.
    """
    if since is not None:
        return f"{state}-since-{since.isoformat()}"
    if limit is None:
        return f"{state}-all"
    return f"{state}-latest-{limit}"


def _dataset_path(
    repo: str,
    state: str,
    limit: int | None,
    since: datetime.date | None,
) -> pathlib.Path:
    """Name the dataset file after the repository and the selector.

    Args:
        repo: The repository as `owner/repo`.
        state: open, closed or all.
        limit: How many were kept, or None.
        since: The date cutoff, or None.

    Returns:
        The path to write, inside DATASET_DIR.
    """
    DATASET_DIR.mkdir(exist_ok=True)
    slug = re.sub(r"[^a-z0-9]+", "-", repo.lower()).strip("-")
    return DATASET_DIR / f"{slug}-{_selector_slug(state, limit, since)}.json"


def parse_repo(target: str) -> str:
    """Read `owner/repo` out of whatever the caller passed.

    Accepts the bare form and a GitHub URL, so a pasted browser address works.

    Args:
        target: `owner/repo` or an https GitHub URL naming one.

    Returns:
        The repository as `owner/repo`.

    Raises:
        SystemExit: If the target names no repository.
    """
    cleaned = target.strip().rstrip("/")
    if cleaned.startswith("https://github.com/"):
        parts = cleaned.removeprefix("https://github.com/").split("/")
        if len(parts) >= 2:
            cleaned = f"{parts[0]}/{parts[1]}"

    if not REPO_PATTERN.match(cleaned):
        sys.exit(f"expected owner/repo, got {target!r}")
    return cleaned


def parse_since(value: str | None) -> datetime.date | None:
    """Read an ISO date, or a plain day count to go back.

    Args:
        value: `YYYY-MM-DD`, or a number of days such as `30`, or None.

    Returns:
        The cutoff date, or None when the caller passed nothing.

    Raises:
        SystemExit: If the value is neither a date nor a day count.
    """
    if value is None:
        return None
    if value.isdigit():
        # UTC, because the created_at timestamps this gets compared against are
        # UTC. A local midnight would move the cutoff by a day either side.
        today = datetime.datetime.now(datetime.UTC).date()
        return today - datetime.timedelta(days=int(value))
    try:
        return datetime.date.fromisoformat(value)
    except ValueError:
        sys.exit(f"--since wants YYYY-MM-DD or a number of days, got {value!r}")


def build_dataset(
    repo: str,
    state: str = "open",
    limit: int | None = DEFAULT_LIMIT,
    since: datetime.date | None = None,
    out: pathlib.Path | None = None,
) -> pathlib.Path:
    """Fetch pull requests and write one dataset file.

    Args:
        repo: The repository as `owner/repo`.
        state: open, closed or all.
        limit: How many of the most recent to keep, or None for every one the
            selector allows.
        since: Keep only pull requests opened on or after this date.
        out: Where to write, or None to name the file after the selector.

    Returns:
        The path written.
    """
    token = _token()
    entries = _list_pulls(repo, token, state, limit, since)
    logger.info(f"Selected {len(entries)} pull requests, now reading each one")

    records = [_pull_record(repo, entry, token) for entry in entries]
    dataset = {
        "repo": repo,
        "fetched_at": datetime.datetime.now(datetime.UTC).isoformat(timespec="seconds"),
        "selector": {
            "state": state,
            "limit": limit,
            "since": since.isoformat() if since else None,
        },
        "pull_requests": records,
    }

    path = out or _dataset_path(repo, state, limit, since)
    path.write_text(json.dumps(dataset, indent=2) + "\n", encoding="utf-8")
    size_kb = path.stat().st_size / 1024
    logger.info(f"Wrote {len(records)} pull requests to {path} ({size_kb:.0f} KB)")
    return path


def main() -> None:
    """Parse arguments and build the dataset."""
    parser = argparse.ArgumentParser(
        description="Build a triage dataset from one repository's pull requests.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Example usage:
    # The 10 most recent open pull requests
    uv run fetch_prs.py agentic-community/mcp-gateway-registry

    # The 25 most recent, or every open one
    uv run fetch_prs.py owner/repo --limit 25
    uv run fetch_prs.py owner/repo --all

    # Everything opened on or after a date, or in the last 30 days
    uv run fetch_prs.py owner/repo --since 2026-08-01
    uv run fetch_prs.py owner/repo --since 30

    # Closed pull requests, to check the triage against what review found
    uv run fetch_prs.py owner/repo --state closed --limit 20

Reads GITHUB_TOKEN or GH_TOKEN, and falls back to `gh auth token`. Needs no Jev
key: pr_triage.py does the Jev call against the file this writes.
""",
    )
    parser.add_argument(
        "repo",
        help="Repository as owner/repo, or a GitHub URL naming one",
    )
    parser.add_argument(
        "--limit",
        type=int,
        default=DEFAULT_LIMIT,
        help=f"How many of the most recent to take (default: {DEFAULT_LIMIT})",
    )
    parser.add_argument(
        "--all",
        action="store_true",
        help="Take every pull request the selector allows, ignoring --limit",
    )
    parser.add_argument(
        "--since",
        help="Keep pull requests opened on or after YYYY-MM-DD, or in the last N days",
    )
    parser.add_argument(
        "--state",
        default="open",
        choices=("open", "closed", "all"),
        help="Which pull requests to list (default: open)",
    )
    parser.add_argument(
        "--out",
        type=pathlib.Path,
        help="Where to write the dataset (default: data/<repo>-<selector>.json)",
    )
    parser.add_argument(
        "--debug",
        action="store_true",
        help="Turn on debug logging",
    )
    args = parser.parse_args()

    if args.debug:
        logging.getLogger().setLevel(logging.DEBUG)

    build_dataset(
        repo=parse_repo(args.repo),
        state=args.state,
        limit=None if args.all else args.limit,
        since=parse_since(args.since),
        out=args.out,
    )


if __name__ == "__main__":
    main()
