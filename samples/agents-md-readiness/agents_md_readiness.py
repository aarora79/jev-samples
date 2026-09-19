"""Score an AGENTS.md or CLAUDE.md against the agents.md format in one call.

Reads the file from disk or from a GitHub URL, sends it to Jev as state, and
asks every question in questions.yml at once: two Choices, four Scores and
eleven Nouls. The format requires no fields, so the checks cover the sections
agents.md recommends and the properties that decide whether an agent can act on
the file.

Both filenames hold the same kind of document, so a path or a GitHub file page
reads whatever it names, and a repo root tries AGENTS.md then CLAUDE.md.

The payload lives in questions.yml. The judgment lives here: WEIGHTS turns four
Scores into one readiness number, and each threshold sits beside the action it
guards.

Usage:
    uv run agents_md_readiness.py                                  # ./AGENTS.md or ./CLAUDE.md
    uv run agents_md_readiness.py path/to/CLAUDE.md                # any local file
    uv run agents_md_readiness.py https://github.com/apache/airflow  # a GitHub repo
    uv run agents_md_readiness.py --verbose                        # raw answers too
"""

import argparse
import json
import logging
import os
import pathlib
import re
import sys
import urllib.error
import urllib.request
from typing import NamedTuple

import yaml
from typesafe_sdk import (
    Choice,
    ChoiceAnswer,
    Noul,
    NoulAnswer,
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
# `uv run agents_md_readiness.py` finds it from any working directory.
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

# The two names an agent instruction file goes by, tried in this order when the
# target names a directory or a GitHub repo rather than one file. Pass a path or
# a file URL and the sample reads it whatever it is called.
DEFAULT_TARGETS: tuple[str, ...] = ("AGENTS.md", "CLAUDE.md")

GITHUB_REPO_URL_PART_COUNT: int = 5

HTTP_NOT_FOUND: int = 404

# A word for each band of credit, highest floor first. Credit is what a question
# contributed toward readiness, from 0 to 1, so these read the same way whether
# the question was a Choice, a Score or a Noul.
JUDGEMENTS: tuple[tuple[float, str], ...] = (
    (0.85, "strong"),
    (0.60, "adequate"),
    (0.35, "thin"),
    (0.15, "weak"),
    (0.00, "missing"),
)

# The same bands for an inverted question, where credit means the thing stayed
# out of the file. "Missing" would read as praise on a leaked credential.
JUDGEMENTS_INVERTED: tuple[tuple[float, str], ...] = (
    (0.85, "clean"),
    (0.60, "probably clean"),
    (0.35, "suspect"),
    (0.15, "likely present"),
    (0.00, "present"),
)

# Confidence under this reads as a coin toss, and the table says so. Jev reports
# confidence for a Choice and a Score; a Noul near 0.5 gets the same treatment.
UNSURE_BELOW: float = 0.5
UNSURE_BAND: tuple[float, float] = (0.35, 0.65)

# The table columns, in order. Padded on print, so the same text reads in a
# terminal and pastes into markdown.
TABLE_HEADER: list[str] = ["Key", "Label", "Type", "Jev returned", "Weight", "Credit", "Judgement"]

# Every run writes one JSON report here, named for the repo and the file it read.
# The folder holds the committed runs behind the table in README.md, so re-running
# a repo shows up as a diff against the numbers that README quotes.
REPORT_DIR: pathlib.Path = pathlib.Path(__file__).parent / "data"

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


def _raw_url_candidates(url: str) -> list[str]:
    """List the raw URLs worth trying for one GitHub web URL.

    Args:
        url: An https URL, GitHub or otherwise.

    Returns:
        One raw URL for a file page, one per default name for a repo root, and
        the input unchanged for anything else.
    """
    if "github.com" not in url:
        return [url]

    if "/blob/" in url:  # a file page names the file already
        raw = url.replace("github.com", "raw.githubusercontent.com", 1)
        return [raw.replace("/blob/", "/", 1)]

    parts = url.rstrip("/").split("/")
    if len(parts) == GITHUB_REPO_URL_PART_COUNT:  # a repo root
        owner, repo = parts[3], parts[4]
        base = f"https://raw.githubusercontent.com/{owner}/{repo}/HEAD"
        return [f"{base}/{name}" for name in DEFAULT_TARGETS]

    return [url]


class Document(NamedTuple):
    """One instruction file, or the absence of one.

    Attributes:
        name: Display name of the file, empty when nothing was found.
        source: The path or URL that answered, or the last place tried.
        text: The document, None when nothing was found.
        looked: Every path or URL tried, in order.
    """

    name: str
    source: str
    text: str | None
    looked: tuple[str, ...]


def _fetch_first(candidates: list[str]) -> tuple[str, str] | None:
    """Fetch the first candidate URL that exists.

    Args:
        candidates: Raw URLs to try in order.

    Returns:
        Tuple of (the URL that answered, its text), or None when every candidate
        returns 404.
    """
    for url in candidates:
        logger.info(f"Fetching: {url}")
        try:
            with urllib.request.urlopen(url, timeout=FETCH_TIMEOUT_SECONDS) as response:  # nosec B310 - https-only, enforced by the caller
                return url, response.read().decode("utf-8", "replace")
        except urllib.error.HTTPError as error:
            if error.code != HTTP_NOT_FOUND:
                raise
            logger.info(f"Not there: {url}")
    return None


def _load_document(target: str | None) -> Document:
    """Load an instruction file from a local path or an https URL.

    A repo or a directory holding neither name is an answer rather than an
    error, so the sample reports it with no readiness instead of stopping. That
    keeps a loop over twenty repos running.

    Args:
        target: A filesystem path, an https URL, or None to look for a default
            name in the current directory.

    Returns:
        The document, with text None when neither name exists.

    Raises:
        SystemExit: If the target is plain http, or names a local file that is
            not there.
    """
    if target is None:
        for name in DEFAULT_TARGETS:
            if pathlib.Path(name).exists():
                target = name
                break
        else:
            looked = tuple(str(pathlib.Path(name).resolve()) for name in DEFAULT_TARGETS)
            return Document("", looked[-1], None, looked)

    if target.startswith("http://"):
        sys.exit("https only: point it at the https:// URL instead")

    if not target.startswith("https://"):
        path = pathlib.Path(target)
        if not path.exists():
            sys.exit(f"no file at {path}")
        logger.info(f"Reading local file: {path}")
        resolved = str(path.resolve())
        return Document(path.name, resolved, path.read_text(encoding="utf-8"), (resolved,))

    candidates = _raw_url_candidates(target)
    answered = _fetch_first(candidates)
    if answered is None:
        return Document("", candidates[-1], None, tuple(candidates))

    url, text = answered
    return Document(url.rsplit("/", 1)[-1], url, text, (url,))


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


def _describe_score(answer: ScoreAnswer) -> str:
    """Name the rubric level a score sits closest to, using Jev's own legend.

    Args:
        answer: The Score answer, carrying score, confidence, legend and
            probabilities.

    Returns:
        The nearest level and the text written for it.
    """
    # The SDK keys legend and probabilities by integer level, counting from 0 in
    # the order the criteria were written.
    nearest = min(round(answer.score), len(answer.legend) - 1)
    return f'nearest level {nearest} "{answer.legend[nearest]}"'


def _credit(
    spec: dict,
    answer: NoulAnswer | ChoiceAnswer | ScoreAnswer,
) -> float:
    """Turn one answer into what it contributed toward readiness, 0 to 1.

    A Score divides by its own top level. A Noul is its probability, or one minus
    it when the entry sets `invert`, so a leaked credential costs readiness. A
    Choice reads the `credit` table the entry carries.

    Args:
        spec: The question entry from questions.yml.
        answer: The answer Jev returned for it.

    Returns:
        Credit from 0 to 1.
    """
    if spec["type"] == "noul":
        return 1.0 - answer.noul if spec.get("invert") else answer.noul
    if spec["type"] == "score":
        return answer.score / (len(answer.legend) - 1)
    return float(spec.get("credit", {}).get(answer.choice, 0.0))


def _weighted(specs: dict) -> dict:
    """Pick out the questions that carry a weight, in file order.

    Args:
        specs: Question entries from questions.yml, keyed by id.

    Returns:
        The weighted entries, keyed by id.
    """
    return {qid: spec for qid, spec in specs.items() if spec.get("weight")}


def _readiness(
    answers: dict,
    specs: dict,
) -> float:
    """Weight every weighted answer into one number, from 0 to 1.

    Jev cannot do arithmetic, so the weighting happens here. Dividing by the
    weights actually present means an edited weight needs no rebalancing.

    Args:
        answers: Answers keyed by question id, as returned by Jev.
        specs: Question entries from questions.yml, keyed by id.

    Returns:
        The weighted readiness, from 0 to 1.
    """
    weighted = _weighted(specs)
    total = sum(spec["weight"] for spec in weighted.values())
    earned = sum(spec["weight"] * _credit(spec, answers[qid]) for qid, spec in weighted.items())
    return earned / total


def _judgement(
    spec: dict,
    answer: NoulAnswer | ChoiceAnswer | ScoreAnswer,
) -> str:
    """Read one answer as a word, and flag the ones Jev was unsure about.

    Args:
        spec: The question entry from questions.yml.
        answer: The answer Jev returned for it.

    Returns:
        A word for the credit the answer earned, with "unsure" appended when Jev
        hedged.
    """
    credit = _credit(spec, answer)
    bands = JUDGEMENTS_INVERTED if spec.get("invert") else JUDGEMENTS
    wording = next(word for floor, word in bands if credit >= floor)

    if spec["type"] == "noul":
        unsure = UNSURE_BAND[0] <= answer.noul <= UNSURE_BAND[1]
    else:
        unsure = answer.confidence < UNSURE_BELOW
    return f"{wording}, unsure" if unsure else wording


def _returned(answer: NoulAnswer | ChoiceAnswer | ScoreAnswer) -> str:
    """Say what Jev returned for one question, in its own terms.

    Args:
        answer: The answer Jev returned.

    Returns:
        The choice and its confidence, the score over its top level and its
        confidence, or the bare Noul probability.
    """
    if answer.type == "noul":
        return f"{answer.noul:.2f}"
    if answer.type == "score":
        top = len(answer.legend) - 1
        return f"{answer.score:.1f} / {top}, confidence {answer.confidence:.2f}"
    return f"{answer.choice}, confidence {answer.confidence:.2f}"


def _print_raw(response: SystemOneResponse) -> None:
    """Pretty print everything Jev returned, before the sample reads any of it.

    Args:
        response: The response object the SDK built from the wire body.
    """
    print("\nRaw response:")
    print(json.dumps(response.model_dump(mode="json"), indent=2))


def _print_scores(
    answers: dict,
    specs: dict,
) -> None:
    """Print each Score against the legend Jev returned with it.

    Args:
        answers: Answers keyed by question id, as returned by Jev.
        specs: Question entries from questions.yml, keyed by id.
    """
    for question_id, spec in specs.items():
        if spec["type"] != "score":
            continue
        answer = answers[question_id]
        top = len(answer.legend) - 1
        label = spec["label"]
        print(f"  {label:15} {answer.score:.1f} / {top}      (confidence {answer.confidence:.2f})")
        print(f"      {_describe_score(answer)}")
    print()


def _print_checks(
    answers: dict,
    specs: dict,
) -> None:
    """Print every Noul in the payload with the word its probability stands for.

    Args:
        answers: Answers keyed by question id, as returned by Jev.
        specs: Question entries from questions.yml, keyed by id and in file
            order, which is the order this prints them.
    """
    print("  agents.md checklist")
    for question_id, spec in specs.items():
        if spec["type"] != "noul":
            continue
        value = answers[question_id].noul
        print(f"    {spec['label']:24} {value:.2f}   {_describe_noul(value)}")
    print("      Noul: one probability, and the number is the confidence. The agents.md FAQ")
    print("      says the format requires no fields, so read these as coverage rather than")
    print("      as a pass or a fail.\n")


def _table_rows(
    answers: dict,
    specs: dict,
) -> list[list[str]]:
    """Build one row of cells per question, in file order.

    Args:
        answers: Answers keyed by question id, as returned by Jev.
        specs: Question entries from questions.yml, keyed by id.

    Returns:
        Rows of cells, matching TABLE_HEADER.
    """
    rows = []
    for question_id, spec in specs.items():
        answer = answers[question_id]
        weight = spec.get("weight")
        kind = spec["type"] + (" (inverted)" if spec.get("invert") else "")
        if not weight:  # diagnostic rather than graded
            rows.append(
                [
                    f"`{question_id}`",
                    spec["label"],
                    kind,
                    _returned(answer),
                    "",
                    "",
                    "routes the fix",
                ]
            )
            continue
        rows.append(
            [
                f"`{question_id}`",
                spec["label"],
                kind,
                _returned(answer),
                f"{weight:.2f}",
                f"{_credit(spec, answer):.2f}",
                _judgement(spec, answer),
            ]
        )
    return rows


def _print_table(
    answers: dict,
    specs: dict,
) -> None:
    """Print the whole call as one padded table, then the readiness number.

    The padding makes it readable in a terminal, and the pipes keep it valid
    markdown, so the same text pastes into a pull request.

    Args:
        answers: Answers keyed by question id, as returned by Jev.
        specs: Question entries from questions.yml, keyed by id and in file
            order, which is the row order.
    """
    rows = _table_rows(answers, specs)
    widths = [max(len(cell) for cell in column) for column in zip(TABLE_HEADER, *rows, strict=True)]

    def line(cells: list[str]) -> str:
        padded = (cell.ljust(width) for cell, width in zip(cells, widths, strict=True))
        return f"| {' | '.join(padded)} |"

    print(line(TABLE_HEADER))
    print(f"| {' | '.join('-' * width for width in widths)} |")
    for row in rows:
        print(line(row))

    weighted = _weighted(specs)
    total = sum(spec["weight"] for spec in weighted.values())
    readiness = _readiness(answers, specs)
    print(
        f"\n**Readiness {readiness:.2f} / 1.00**, the weighted average of the credit column "
        f"over {len(weighted)} questions carrying {total:.2f} of weight.\n"
        "Weights live in questions.yml, so raise the one you care about and re-run."
    )


def _print_verdicts(
    answers: dict,
    specs: dict,
) -> None:
    """Print one verdict per action, each gated at its own threshold.

    Args:
        answers: Answers keyed by question id, as returned by Jev.
        specs: Question entries from questions.yml, keyed by id.
    """
    # A leaked credential costs a key rotation and an audit, so this gate sits
    # low on purpose and fires on a maybe.
    if answers["leaks_secret"].noul > 0.3:
        print("  -> read the file for a credential before you commit it")

    # Missing test commands means an agent skips your tests, which is the one
    # failure the format's FAQ calls out, so the bar for calling it missing is
    # the middle of the range.
    if answers["test_commands"].noul < 0.5:
        print("  -> add the test commands, because an agent runs the ones it finds")

    # Rewriting a document is cheap and reversible, so 0.6 of the readiness
    # range is bar enough to suggest it.
    if _readiness(answers, specs) < 0.6:
        print(f"  -> thin for an agent: start with {answers['weakest_area'].choice}")

    written_for = answers["written_for"]
    if written_for.choice != "agent" and written_for.confidence > 0.7:
        print(f"  -> this reads as {written_for.choice}, so an agent gets no instructions from it")


def _print_answers(
    name: str,
    source: str,
    answers: dict,
    specs: dict,
) -> None:
    """Print every answer, what the number means, then one verdict per action.

    Every explanation comes out of the answer itself: the probabilities Jev
    spread across the options, the legend it returns with a Score, and the
    confidence it reports for both.

    Args:
        name: Display name of the document that was read.
        source: The path or URL the text came from.
        answers: Answers keyed by question id, as returned by Jev.
        specs: Question entries from questions.yml, keyed by id.
    """
    written_for = answers["written_for"]
    weakest = answers["weakest_area"]
    print(f"\n{name}  ({source})\n")

    label = specs["written_for"]["label"]
    print(f"  {label:15} {written_for.choice:12} (confidence {written_for.confidence:.2f})")
    print(f"      Choice: one label out of {len(written_for.probabilities)},")
    print(f"      scored {_describe_choice(written_for)}.\n")

    _print_scores(answers, specs)
    _print_checks(answers, specs)

    label = specs["weakest_area"]["label"]
    print(f"  {label:15} {weakest.choice:12} (confidence {weakest.confidence:.2f})")
    print(f"      Choice: {_describe_choice(weakest)}.")
    print("      Jev ranks the five areas and names one even when every area is strong,")
    print("      so read this next to the readiness number rather than on its own.\n")

    _print_verdicts(answers, specs)
    print()
    _print_table(answers, specs)


def _slug(text: str) -> str:
    """Reduce a name to lowercase words joined by hyphens.

    Args:
        text: A repo name, a filename, or anything else headed for a path.

    Returns:
        The slug, with every run of other characters collapsed to one hyphen.
    """
    return re.sub(r"[^a-z0-9]+", "-", text.lower()).strip("-")


def _report_path(
    document: Document,
) -> pathlib.Path:
    """Name the JSON report after the repo and the file that was read.

    A raw GitHub URL gives owner and repo. A local path gives the directory the
    file sits in, which is the repo for a checkout. A document that was never
    found is named for where the sample looked.

    Args:
        document: The document, found or missing.

    Returns:
        The path to write, inside REPORT_DIR.
    """
    raw_prefix = "https://raw.githubusercontent.com/"
    if document.source.startswith(raw_prefix):
        owner, repo = document.source.removeprefix(raw_prefix).split("/")[:2]
        project = f"{owner}-{repo}"
    else:
        project = pathlib.Path(document.source).parent.name or "local"
    suffix = _slug(document.name) if document.text is not None else "not-found"
    REPORT_DIR.mkdir(exist_ok=True)
    return REPORT_DIR / f"{_slug(project)}-{suffix}.json"


def _write_missing_report(document: Document) -> pathlib.Path:
    """Write the report for a repo or directory holding neither name.

    Readiness is null rather than zero. Zero would say the document failed every
    check, and no document was there to fail one.

    Args:
        document: The document, with text None.

    Returns:
        The path written.
    """
    report = {
        "filename": None,
        "source": document.source,
        "found": False,
        "looked": list(document.looked),
        "model": None,
        "usage": None,
        "readiness": None,
        "questions": {},
    }
    path = _report_path(document)
    path.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    return path


def _print_missing(document: Document) -> None:
    """Say that neither name was there, and that readiness does not apply.

    Args:
        document: The document, with text None.
    """
    print(f"\nno {' or '.join(DEFAULT_TARGETS)}\n")
    for place in document.looked:
        print(f"  looked at       {place}")
    print("\n  readiness       None (not available)")
    print("      Nothing reached Jev, so no question ran and no readiness exists.")
    print("      None is not zero: zero would say the document failed every check.")


def _write_report(
    document: Document,
    response: SystemOneResponse,
    specs: dict,
) -> pathlib.Path:
    """Write one JSON report: every answer as Jev sent it, plus the judgment.

    Args:
        document: The document that was read.
        response: The response object the SDK built from the wire body.
        specs: Question entries from questions.yml, keyed by id.

    Returns:
        The path written.
    """
    answers = response.answers
    report = {
        "filename": document.name,
        "source": document.source,
        "found": True,
        "looked": list(document.looked),
        "model": response.model,
        "usage": response.usage.model_dump(mode="json"),
        "readiness": round(_readiness(answers, specs), 4),
        "questions": {
            question_id: {
                "label": spec["label"],
                "type": spec["type"],
                "weight": spec.get("weight"),
                "inverted": bool(spec.get("invert")),
                "returned": answers[question_id].model_dump(mode="json"),
                "credit": round(_credit(spec, answers[question_id]), 4)
                if spec.get("weight")
                else None,
                "judgement": _judgement(spec, answers[question_id]) if spec.get("weight") else None,
            }
            for question_id, spec in specs.items()
        },
    }

    path = _report_path(document)
    path.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    return path


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


def score_readiness(
    target: str | None,
    verbose: bool = False,
) -> None:
    """Score one instruction file, asking Jev every question in the payload.

    A repo or directory holding neither AGENTS.md nor CLAUDE.md reports no
    readiness and never reaches the API.

    Args:
        target: A filesystem path, an https URL, or None for the current
            directory.
        verbose: Print the raw answers before the explained summary.
    """
    settings, specs = _load_payload()
    document = _load_document(target)

    if document.text is None:
        _print_missing(document)
        print(f"\nReport: {_write_missing_report(document)}")
        return

    _require_api_key()
    questions = {
        question_id: _build_question(question_id, spec) for question_id, spec in specs.items()
    }
    response = TypeSafeClient().system_one(
        model=settings["model"],
        # Treat the document as untrusted. A file that argues for its own
        # completeness moves these answers, so the state names what it is.
        # The filename goes in the state, so a CLAUDE.md is not read as an
        # AGENTS.md and the model knows which convention it is looking at.
        state={
            "filename": document.name,
            "agent_instructions": document.text[: settings["max_state_chars"]],
        },
        questions=questions,
    )
    logger.debug(
        f"Asked {len(questions)} questions, used {response.usage.input_tokens} input tokens"
    )

    if verbose:
        _print_raw(response)

    _print_answers(document.name, document.source, response.answers, specs)
    print(f"\nReport: {_write_report(document, response, specs)}")


def main() -> None:
    """Parse arguments and run the check."""
    parser = argparse.ArgumentParser(
        description="Score an AGENTS.md or CLAUDE.md against the agents.md format in one call.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Example usage:
    # The AGENTS.md or CLAUDE.md in the current directory
    uv run agents_md_readiness.py

    # Any local file, whatever it is called
    uv run agents_md_readiness.py path/to/CLAUDE.md

    # A GitHub repo root, which tries AGENTS.md then CLAUDE.md
    uv run agents_md_readiness.py https://github.com/apache/airflow

    # A GitHub file page names the file itself
    uv run agents_md_readiness.py https://github.com/owner/repo/blob/main/CLAUDE.md

    # Print the raw answers Jev returned, then the explained summary
    uv run agents_md_readiness.py --verbose https://github.com/apache/airflow

The questions, the model pin and the state budget live in questions.yml.
Reads TYPESAFE_API_KEY from the environment, or from .env beside this file or at
the repo root.
""",
    )
    parser.add_argument(
        "target",
        nargs="?",
        help=f"Local path or https URL to read (default: ./{' or ./'.join(DEFAULT_TARGETS)})",
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

    score_readiness(args.target, args.verbose)


if __name__ == "__main__":
    main()
