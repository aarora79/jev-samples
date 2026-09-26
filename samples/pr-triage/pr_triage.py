"""Triage a repository's open pull requests, one Jev call each.

Reads a dataset built by fetch_prs.py, or builds one on the spot, and asks Jev
eleven questions about every pull request in it: two Choices, three Scores and
six Nouls. Nine of them carry a weight, and the weighted average is the review
load, which lands each pull request in a tier from trivial to high.

The payload lives in questions.yml. The judgment lives here: the weighting, the
size floor that stops a 58-file pull request being called trivial, and the
thresholds behind each tier's advice.

Usage:
    uv run pr_triage.py owner/repo                       # 10 most recent open PRs
    uv run pr_triage.py owner/repo --limit 25            # the 25 most recent
    uv run pr_triage.py owner/repo --since 2026-08-01    # opened on or after a date
    uv run pr_triage.py --dataset data/some-repo.json    # replay a saved dataset
    uv run pr_triage.py --dataset data/x.json --explain 1693   # one PR, every question
"""

import argparse
import datetime
import json
import logging
import os
import pathlib
import re
import sys
import time

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

from fetch_prs import build_dataset, parse_since, parse_target

# Configure logging with basicConfig
logging.basicConfig(
    level=logging.INFO,  # Set the log level to INFO
    # Define log message format
    format="%(asctime)s,p%(process)s,{%(filename)s:%(lineno)d},%(levelname)s,%(message)s",
)
logger = logging.getLogger(__name__)

# The model pin, the state budgets and every question. Sits beside this file so
# `uv run pr_triage.py` finds it from any working directory.
PAYLOAD_FILE: pathlib.Path = pathlib.Path(__file__).parent / "questions.yml"

# The SDK reads the key from this variable.
API_KEY_ENV: str = "TYPESAFE_API_KEY"

# Where to look when the environment has no key: beside the sample, then at the
# repo root. The sample reads one variable out of the file and logs the path it
# came from, never the value.
ENV_FILES: tuple[pathlib.Path, ...] = (
    pathlib.Path(__file__).parent / ".env",
    pathlib.Path(__file__).parents[2] / ".env",
)

# Every run writes one JSON report here, named for the repo and the selector.
REPORT_DIR: pathlib.Path = pathlib.Path(__file__).parent / "data"

# The tiers, cheapest review first. Index order is what makes the size floor
# below able to raise a tier without a second table.
TIERS: tuple[str, ...] = ("trivial", "low", "medium", "high")

# What review load buys which tier, highest floor first. The cuts come from the
# cost of being wrong at each one: calling a medium pull request low costs a
# reviewer an hour, and calling a high one medium costs an incident.
LOAD_FLOORS: tuple[tuple[float, str], ...] = (
    (0.62, "high"),
    (0.42, "medium"),
    (0.22, "low"),
    (0.00, "trivial"),
)

# The lowest tier a pull request of a given size can land in, whatever Jev
# returned. Jev sees a truncated diff, so a 58-file change can read as nine
# repeated edits and score as mechanical. Size is arithmetic, Python does it,
# and it only ever raises a tier.
#
# Each entry is (files, lines, floor): more than this many files, or more than
# this many lines changed, and the tier starts here. Highest floor first.
SIZE_FLOORS: tuple[tuple[int, int, str], ...] = (
    (30, 1500, "high"),
    (12, 500, "medium"),
    (4, 150, "low"),
)

# What to do with a pull request in each tier. The advice is the point of the
# triage, so it sits next to the tier rather than in a README.
TIER_ADVICE: dict[str, str] = {
    "trivial": "merge on a glance: read the title, skim the diff, check that CI is green",
    "low": "one reviewer, one pass, no meeting",
    "medium": "one reviewer who knows this area, reading the whole diff",
    "high": "a human reads this line by line, and the author walks them through it",
}

# A word for each band of a Noul probability, highest floor first. Jev returns
# one number and no separate confidence, so 0.5 says the diff never settled the
# question either way.
NOUL_WORDS: tuple[tuple[float, str], ...] = (
    (0.9, "yes"),
    (0.7, "probably yes"),
    (0.3, "unsettled"),
    (0.1, "probably no"),
    (0.0, "no"),
)

# Confidence under this reads as a coin toss, and the table says so. Jev reports
# confidence for a Choice and a Score; a Noul near 0.5 gets the same treatment.
UNSURE_BELOW: float = 0.5
UNSURE_BAND: tuple[float, float] = (0.35, 0.65)

# Repeat calls on one pull request moved a load by up to 0.03, so a value this
# near a tier cut can land on either side of it between runs. Within this
# distance the tier column says so, and the unsure markers widen by the same
# amount, because over-flagging doubt costs a reader nothing.
DEADBAND: float = 0.02

# A question contributing at least this much of the load gets named as a driver.
# Below it the line turns into a list of everything, which names nothing.
DRIVER_FLOOR: float = 0.05

# Coverage is the share of changed files whose patch fit in the diff budget.
# Under this, Jev read a sample of the change rather than the change, and the
# output says so: a load of 0.37 across 24 of 54 files is a weaker claim than the
# same number across all of them. This is why the size floor exists.
LOW_COVERAGE_BELOW: float = 0.70

# How many drivers to name per pull request.
DRIVER_COUNT: int = 3

# The four questions that say what breaks if this change is wrong, as opposed to
# how long it takes to read. Consequence is the MAX of these, never the mean: a
# change that is safe in three ways and dangerous in one is a dangerous change,
# and averaging lets three absences of risk vote down one confident yes.
CONSEQUENCE_QUESTIONS: tuple[str, ...] = (
    "security_surface",
    "breaking_change",
    "blast_radius",
    "infra_surface",
)

# What evidence is enough, cheapest first. The route names what has to be true
# before this merges, and nothing here ever says "merge": TypeSafe publishes 67.8%
# accuracy on their own benchmark, which is fine for deciding how much evidence to
# demand and nowhere near enough to be the last gate before main.
ROUTES: tuple[str, ...] = (
    "green-is-enough",
    "tests-are-enough",
    "ai-review-is-enough",
    "human-required",
    "human-plus-author",
)

# What consequence buys which route, highest floor first. The cuts come from the
# cost of being wrong: a change that probably touches auth cannot be waved through
# on a green tick, whatever else is true of it.
CONSEQUENCE_FLOORS: tuple[tuple[float, str], ...] = (
    (0.60, "human-required"),
    (0.35, "ai-review-is-enough"),
    (0.15, "tests-are-enough"),
    (0.00, "green-is-enough"),
)

# What each route asks of a team, so the advice travels with the route rather than
# living in a README.
ROUTE_ADVICE: dict[str, str] = {
    "green-is-enough": "merge when CI is green: nothing here needs a person",
    "tests-are-enough": "merge when CI is green and a test exercises the change",
    "ai-review-is-enough": "an AI review that finds nothing is sufficient, plus green CI and tests",
    "human-required": "a person reads this before it merges, whatever the machines say",
    "human-plus-author": "a person reads it line by line, and the author walks them through it",
}

# Effort can only raise the route, never lower it. A long read wants a second pair
# of eyes even when nothing dangerous is in it, and a long read on top of a high
# consequence wants the author in the room.
EFFORT_RAISES: tuple[tuple[float, str], ...] = (
    (0.62, "ai-review-is-enough"),
    (0.42, "tests-are-enough"),
)

TRIAGE_HEADER: list[str] = [
    "PR",
    "Files",
    "Lines",
    "Kind",
    "Load",
    "Cons",
    "Route",
    "Title",
]

DETAIL_HEADER: list[str] = [
    "Key",
    "Label",
    "Type",
    "Jev returned",
    "Weight",
    "Credit",
    "Adds",
    "Reading",
]

# Titles longer than this get cut in the table. The grouped section below it
# prints every title whole.
MAX_TITLE_CHARS: int = 58


def _load_payload() -> tuple[dict, dict]:
    """Read the settings and the questions from questions.yml.

    Returns:
        Tuple of (settings, specs). Settings holds the model, the three state
        budgets and the input-token price; specs holds each question entry keyed
        by id, in file order.

    Raises:
        SystemExit: If the file is missing a key the request needs.
    """
    payload = yaml.safe_load(PAYLOAD_FILE.read_text(encoding="utf-8"))
    wanted = {
        "model",
        "max_description_chars",
        "max_file_list_chars",
        "max_diff_chars",
        "input_usd_per_million",
        "questions",
    }
    missing = wanted - payload.keys()
    if missing:
        sys.exit(f"{PAYLOAD_FILE.name}: missing {', '.join(sorted(missing))}")

    settings = {key: payload[key] for key in wanted - {"questions"}}
    return settings, payload["questions"]


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


def _file_list_text(
    files: list[dict],
    budget: int,
) -> str:
    """List every changed path with its line counts, one per line.

    Cheap in tokens and dense in signal: the paths alone say whether a change is
    docs, tests, infrastructure or product code, and they survive a diff that
    got truncated.

    Args:
        files: File records from the dataset.
        budget: Character budget for the whole list.

    Returns:
        The list, cut at the budget on a line boundary.
    """
    lines = [
        f"{entry['status']:9} +{entry['additions']:<5} -{entry['deletions']:<5} {entry['path']}"
        for entry in files
    ]
    text = "\n".join(lines)
    if len(text) <= budget:
        return text

    # Cut on a line boundary, so the last entry a reader sees is a whole path
    # rather than half of one.
    kept = text[:budget].rsplit("\n", 1)[0]
    shown = kept.count("\n") + 1
    return f"{kept}\n... and {len(files) - shown} more paths"


def _diff_text(
    files: list[dict],
    budget: int,
) -> tuple[str, int]:
    """Assemble the patches into one diff, fitting as many whole files as it can.

    Smallest patches first. Triage asks how far a change reaches and whether the
    same edit repeats, and both of those want breadth: twelve files read in full
    answer them, where one 5,000-line lockfile answers neither. The file list
    above carries the paths that did not fit, and the state says how many.

    Args:
        files: File records from the dataset.
        budget: Character budget for the whole diff.

    Returns:
        Tuple of (diff, files shown).
    """
    with_patch = [entry for entry in files if entry["has_patch"]]
    chunks: list[str] = []
    used = 0

    for entry in sorted(with_patch, key=lambda item: len(item["patch"])):
        chunk = f"diff --git a/{entry['path']} b/{entry['path']}\n{entry['patch']}\n"
        if used + len(chunk) > budget:
            break
        chunks.append(chunk)
        used += len(chunk)

    return "".join(chunks), len(chunks)


def _build_state(
    repo: str,
    pull: dict,
    settings: dict,
) -> tuple[dict, float]:
    """Build the state for one pull request, each part in its own named field.

    The counts go in as a sentence rather than as numbers to compare. Jev cannot
    do arithmetic, so the sentence is context and every threshold on a count
    happens in Python.

    Args:
        repo: The repository as `owner/repo`.
        pull: One pull request record from the dataset.
        settings: Settings from questions.yml, holding the three budgets.

    Returns:
        Tuple of (state, coverage). Coverage is the share of changed files whose
        patch fit the budget, which says how much of the change Jev read.
    """
    files = pull["files"]
    diff, shown = _diff_text(files, settings["max_diff_chars"])
    omitted = len(files) - shown
    coverage = shown / len(files) if files else 1.0
    scope = (
        f"{pull['changed_files']} files changed, "
        f"{pull['additions']} lines added, {pull['deletions']} removed"
    )
    if omitted > 0:
        scope += (
            f". The diff below holds {shown} of those files; {omitted} are listed without a patch"
        )

    state = {
        "repo": repo,
        "base_branch": pull["base"],
        "title": pull["title"],
        # Treat the description as untrusted. An author who writes "trivial,
        # please merge" moves these answers, so the state names what the text is
        # rather than presenting it as fact.
        "description_written_by_the_author": (pull["body"] or "(no description)")[
            : settings["max_description_chars"]
        ],
        "scope": scope,
        "changed_files": _file_list_text(files, settings["max_file_list_chars"]),
        "diff": diff or "(no textual diff: binary files, or none GitHub would render)",
    }
    return state, coverage


def _credit(
    spec: dict,
    answer: NoulAnswer | ChoiceAnswer | ScoreAnswer,
) -> float:
    """Turn one answer into what it contributed toward review load, 0 to 1.

    A Score divides by its own top level. A Noul is its probability. Either one
    flips when the entry sets `invert`, so a mechanical diff and a thorough
    description lower the load instead of raising it. A Choice reads the `credit`
    table the entry carries, and carries none today.

    Args:
        spec: The question entry from questions.yml.
        answer: The answer Jev returned for it.

    Returns:
        Credit from 0 to 1, where 1 is the most review.
    """
    if spec["type"] == "noul":
        value = answer.noul
    elif spec["type"] == "score":
        value = answer.score / (len(answer.legend) - 1)
    else:
        value = float(spec.get("credit", {}).get(answer.choice, 0.0))

    return 1.0 - value if spec.get("invert") else value


def _weighted(specs: dict) -> dict:
    """Pick out the questions that carry a weight, in file order.

    Args:
        specs: Question entries from questions.yml, keyed by id.

    Returns:
        The weighted entries, keyed by id.
    """
    return {qid: spec for qid, spec in specs.items() if spec.get("weight")}


def _review_load(
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
        The review load, from 0 to 1.
    """
    weighted = _weighted(specs)
    total = sum(spec["weight"] for spec in weighted.values())
    earned = sum(spec["weight"] * _credit(spec, answers[qid]) for qid, spec in weighted.items())
    return earned / total


def _contribution(
    spec: dict,
    answer: NoulAnswer | ChoiceAnswer | ScoreAnswer,
    total_weight: float,
) -> float:
    """Say how much of the review load one answer accounts for.

    These sum to the load across every weighted question, so the Adds column of
    the detail table adds up to the number at the bottom of it.

    Args:
        spec: The question entry from questions.yml.
        answer: The answer Jev returned for it.
        total_weight: The weights of every weighted question, summed.

    Returns:
        The share of the load, from 0 to the question's weight.
    """
    return spec["weight"] * _credit(spec, answer) / total_weight


def _size_floor(
    changed_files: int,
    lines: int,
) -> str:
    """Name the lowest tier a pull request of this size can land in.

    Args:
        changed_files: How many files the pull request touches.
        lines: Additions plus deletions.

    Returns:
        A tier name, "trivial" when the change is small enough to have no floor.
    """
    for files_over, lines_over, floor in SIZE_FLOORS:
        if changed_files > files_over or lines > lines_over:
            return floor
    return TIERS[0]


def _tier(
    load: float,
    changed_files: int,
    lines: int,
) -> tuple[str, bool]:
    """Place one pull request in a tier, and say whether size did the placing.

    Args:
        load: The review load, from 0 to 1.
        changed_files: How many files the pull request touches.
        lines: Additions plus deletions.

    Returns:
        Tuple of (tier, raised by size).
    """
    from_load = next(tier for floor, tier in LOAD_FLOORS if load >= floor)
    floor = _size_floor(changed_files, lines)

    if TIERS.index(floor) > TIERS.index(from_load):
        return floor, True
    return from_load, False


def _near_a_cut(load: float) -> bool:
    """Say whether a load sits close enough to a tier cut to move between runs.

    Args:
        load: The review load, from 0 to 1.

    Returns:
        True when the load is within DEADBAND of any cut in LOAD_FLOORS.
    """
    return any(floor > 0.0 and round(abs(load - floor), 6) <= DEADBAND for floor, _ in LOAD_FLOORS)


def _near_a_route_cut(consequence: float) -> bool:
    """Say whether a consequence sits close enough to a route cut to move between runs.

    The same deadband the tier uses. Repeat calls move a consequence by about this
    much, so a value inside the band would otherwise flip a route between runs and
    two reports of one queue would disagree.

    Args:
        consequence: The consequence, 0 to 1.

    Returns:
        True when the consequence is within DEADBAND of any cut in
        CONSEQUENCE_FLOORS.
    """
    return any(
        cut > 0.0 and round(abs(consequence - cut), 6) <= DEADBAND for cut, _ in CONSEQUENCE_FLOORS
    )


def _consequence(
    answers: dict,
    specs: dict,
) -> tuple[float, str]:
    """Read what breaks if this change is wrong, and name what said so.

    The max rather than the mean, because risk does not average. A smoke alarm
    that averages four rooms is useless.

    Args:
        answers: Answers keyed by question id, as returned by Jev.
        specs: Question entries from questions.yml, keyed by id.

    Returns:
        Tuple of (consequence from 0 to 1, the label that set it).
    """
    worst, label = 0.0, ""
    for qid in CONSEQUENCE_QUESTIONS:
        if qid not in specs or qid not in answers:
            continue
        value = _consequence_of(specs[qid], answers[qid])
        if value >= worst:
            worst, label = value, specs[qid]["label"]
    return worst, label


def _consequence_of(
    spec: dict,
    answer: NoulAnswer | ChoiceAnswer | ScoreAnswer,
) -> float:
    """Read one answer as consequence, which is not the same as credit.

    A Noul reads as its probability. A Score reads as the probability it put on
    its top level, rather than as the normalised score, because only the top level
    of these rubrics describes something consequential. `blast_radius` level 1 is
    "confined to one module or feature area", which is ordinary work, and dividing
    a score of 1.0 by 2 would report ordinary work as half a catastrophe.

    Args:
        spec: The question entry from questions.yml.
        answer: The answer Jev returned for it.

    Returns:
        Consequence from 0 to 1.
    """
    if spec["type"] != "score":
        return _credit(spec, answer)

    top = len(answer.legend) - 1
    # The SDK keys probabilities by int level; a wire-shaped dict keys them by str.
    probabilities = answer.probabilities
    return float(probabilities.get(top, probabilities.get(str(top), 0.0)))


def _route(
    consequence: float,
    load: float,
) -> tuple[str, str]:
    """Say what evidence is enough before this merges, and what decided that.

    Consequence sets the floor and effort can only raise it. Neither can lower
    the other, so a small diff cannot talk its way past an auth change and a
    quiet auth surface cannot wave through a thousand-line refactor.

    Args:
        consequence: The max of the consequence questions, 0 to 1.
        load: The review load, 0 to 1.

    Returns:
        Tuple of (route, "consequence" or "effort" or "both").
    """
    floor = next(route for cut, route in CONSEQUENCE_FLOORS if consequence >= cut)
    index = ROUTES.index(floor)
    decided = "consequence"

    for cut, route in EFFORT_RAISES:
        if load >= cut and ROUTES.index(route) > index:
            index, decided = ROUTES.index(route), "effort"
            break

    # A long read of something dangerous is the one case that wants the author
    # walking a human through it.
    if consequence >= CONSEQUENCE_FLOORS[0][0] and load >= EFFORT_RAISES[0][0]:
        return ROUTES[-1], "both"

    return ROUTES[index], decided


def _downgrade(
    answers: dict,
    specs: dict,
    route: str,
    decided: str,
) -> str:
    """Name the one signal that would move this to a cheaper route.

    A tier tells an author nothing to do. This tells them exactly what to do.

    Args:
        answers: Answers keyed by question id, as returned by Jev.
        specs: Question entries from questions.yml, keyed by id.
        route: The route this pull request landed on.
        decided: What set it, from _route.

    Returns:
        A sentence, or "" when it is already on the cheapest route.
    """
    if route == ROUTES[0]:
        return ""
    if decided == "effort":
        drivers = _drivers(answers, specs)
        biggest = drivers[0] if drivers else "the weighted questions"
        return f"a shorter read, or splitting it: {biggest} is carrying the load"

    _, label = _consequence(answers, specs)
    if label == specs.get("has_tests", {}).get("label"):
        return "tests that exercise the change"
    return f"evidence that {label} is covered, a test or a reviewer who owns it"


def _drivers(
    answers: dict,
    specs: dict,
) -> list[str]:
    """Name the questions that account for most of the review load.

    Args:
        answers: Answers keyed by question id, as returned by Jev.
        specs: Question entries from questions.yml, keyed by id.

    Returns:
        Up to DRIVER_COUNT labels, largest contribution first.
    """
    weighted = _weighted(specs)
    total = sum(spec["weight"] for spec in weighted.values())
    ranked = sorted(
        (
            (_contribution(spec, answers[qid], total), spec["label"])
            for qid, spec in weighted.items()
        ),
        reverse=True,
    )
    return [label for share, label in ranked[:DRIVER_COUNT] if share >= DRIVER_FLOOR]


def _describe_noul(value: float) -> str:
    """Turn one Noul probability into the word it stands for.

    Args:
        value: The probability Jev returned for the statement, from 0 to 1.

    Returns:
        The word for the band the probability falls in.
    """
    return next(wording for floor, wording in NOUL_WORDS if value >= floor)


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


def _reading(
    spec: dict,
    answer: NoulAnswer | ChoiceAnswer | ScoreAnswer,
) -> str:
    """Read one answer in plain words, and flag the ones Jev was unsure about.

    Every word here comes out of the answer: the band a Noul probability falls
    in, and the legend Jev returns with a Score.

    Args:
        spec: The question entry from questions.yml.
        answer: The answer Jev returned for it.

    Returns:
        The reading, with "unsure" appended when Jev hedged.
    """
    if spec["type"] == "noul":
        wording = _describe_noul(answer.noul)
        # Both markers widen by the deadband, so a value wobbling around the cut
        # reads the same way on every run.
        unsure = UNSURE_BAND[0] - DEADBAND <= answer.noul <= UNSURE_BAND[1] + DEADBAND
    elif spec["type"] == "score":
        # The SDK keys legend and probabilities by integer level, counting from 0
        # in the order the criteria were written.
        nearest = min(round(answer.score), len(answer.legend) - 1)
        wording = f'level {nearest} "{answer.legend[nearest]}"'
        unsure = answer.confidence < UNSURE_BELOW + DEADBAND
    else:
        wording = answer.choice
        unsure = answer.confidence < UNSURE_BELOW + DEADBAND

    return f"{wording}, unsure" if unsure else wording


def _padded_lines(
    header: list[str],
    rows: list[list[str]],
) -> list[str]:
    """Build one table padded to its widest cell per column.

    The padding makes it readable in a terminal, and the pipes keep it valid
    markdown, so the same text pastes into a pull request and the same lines go
    into the markdown report.

    Args:
        header: Column titles.
        rows: Cells per row, matching header.

    Returns:
        The header, the rule, then one line per row.
    """
    widths = [max(len(cell) for cell in column) for column in zip(header, *rows, strict=True)]

    def line(cells: list[str]) -> str:
        padded = (cell.ljust(width) for cell, width in zip(cells, widths, strict=True))
        return f"| {' | '.join(padded)} |"

    rule = f"| {' | '.join('-' * width for width in widths)} |"
    return [line(header), rule] + [line(row) for row in rows]


def _print_padded(
    header: list[str],
    rows: list[list[str]],
) -> None:
    """Print one padded table.

    Args:
        header: Column titles.
        rows: Cells per row, matching header.
    """
    for text in _padded_lines(header, rows):
        print(text)


def _triage_one(
    client: TypeSafeClient,
    repo: str,
    pull: dict,
    questions: dict,
    specs: dict,
    settings: dict,
) -> dict:
    """Ask Jev every question about one pull request, then judge the answers.

    Args:
        client: The SDK client, reused across pull requests.
        repo: The repository as `owner/repo`.
        pull: One pull request record from the dataset.
        questions: The SDK question objects, keyed by id.
        specs: Question entries from questions.yml, keyed by id.
        settings: Settings from questions.yml.

    Returns:
        One triage result, holding the answers, the load, the tier and the cost.
    """
    state, coverage = _build_state(repo, pull, settings)
    started = time.perf_counter()
    response: SystemOneResponse = client.system_one(
        model=settings["model"],
        state=state,
        questions=questions,
    )
    latency_ms = round((time.perf_counter() - started) * 1000)

    answers = response.answers
    lines = pull["additions"] + pull["deletions"]
    load = _review_load(answers, specs)
    consequence, consequence_label = _consequence(answers, specs)
    route, decided = _route(consequence, load)
    tier, raised_by_size = _tier(load, pull["changed_files"], lines)
    logger.info(
        f"#{pull['number']}: load {load:.2f}, consequence {consequence:.2f}, "
        f"route {route}, {latency_ms} ms"
    )

    return {
        "pull": pull,
        "response": response,
        "answers": answers,
        "lines": lines,
        "load": load,
        "consequence": consequence,
        "consequence_label": consequence_label,
        "route": route,
        "route_decided_by": decided,
        "downgrade": _downgrade(answers, specs, route, decided),
        "tier": tier,
        "raised_by_size": raised_by_size,
        "coverage": coverage,
        "drivers": _drivers(answers, specs),
        "latency_ms": latency_ms,
    }


def _triage_row(result: dict) -> list[str]:
    """Build the triage table row for one pull request.

    Args:
        result: One triage result.

    Returns:
        Cells matching TRIAGE_HEADER.
    """
    pull = result["pull"]
    title = pull["title"]
    if len(title) > MAX_TITLE_CHARS:
        title = title[: MAX_TITLE_CHARS - 3] + "..."

    load = f"{result['load']:.2f}"
    if result["raised_by_size"]:
        load += " (size)"
    elif _near_a_cut(result["load"]):
        load += " (on a cut)"

    return [
        f"#{pull['number']}",
        str(pull["changed_files"]),
        f"+{pull['additions']}/-{pull['deletions']}",
        result["answers"]["change_kind"].choice,
        load,
        f"{result['consequence']:.2f}",
        result["route"] + (" (on a cut)" if _near_a_route_cut(result["consequence"]) else ""),
        title,
    ]


def _print_triage_table(results: list[dict]) -> None:
    """Print one row per pull request, heaviest first.

    Args:
        results: Triage results, in dataset order.
    """
    ordered = sorted(results, key=lambda item: item["load"], reverse=True)
    _print_padded(TRIAGE_HEADER, [_triage_row(result) for result in ordered])


def _print_groups(results: list[dict]) -> None:
    """Print the pull requests grouped by route, with what each route asks for.

    The route is the thing a reader acts on, so it sets the grouping. The tier is
    still in the report, as the effort half of the picture.

    Args:
        results: Triage results, in dataset order.
    """
    for route in reversed(ROUTES):
        members = sorted(
            (result for result in results if result["route"] == route),
            key=lambda item: item["consequence"],
            reverse=True,
        )
        if not members:
            continue

        print(f"\n{route.upper()}  ({len(members)})  -> {ROUTE_ADVICE[route]}")
        for result in members:
            pull = result["pull"]
            drivers = ", ".join(result["drivers"]) or "nothing above the driver floor"
            size_note = "  [size floor]" if result["raised_by_size"] else ""
            print(
                f"  #{pull['number']:<6} consequence {result['consequence']:.2f} "
                f"({result['consequence_label']}), effort {result['load']:.2f}"
                f"{size_note}  {pull['title']}"
            )
            print(f"          drivers: {drivers}")
            if result["downgrade"]:
                print(f"          would drop a route with: {result['downgrade']}")
            if result["coverage"] < LOW_COVERAGE_BELOW:
                print(
                    f"          read from {result['coverage']:.0%} of the changed files, "
                    "so both numbers read part of the diff"
                )
            print(f"          {pull['url']}")


def _print_detail(
    result: dict,
    specs: dict,
) -> None:
    """Print every question for one pull request, and what each one contributed.

    Args:
        result: One triage result.
        specs: Question entries from questions.yml, keyed by id.
    """
    pull = result["pull"]
    answers = result["answers"]
    weighted = _weighted(specs)
    total = sum(spec["weight"] for spec in weighted.values())

    print(f"\n#{pull['number']}  {pull['title']}")
    print(f"{pull['url']}\n")

    rows = []
    for question_id, spec in specs.items():
        answer = answers[question_id]
        weight = spec.get("weight")
        kind = spec["type"] + (" (inverted)" if spec.get("invert") else "")
        if not weight:  # routes the review rather than sizing it
            rows.append(
                [f"`{question_id}`", spec["label"], kind, _returned(answer), "", "", "", "routes"]
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
                f"{_contribution(spec, answer, total):.3f}",
                _reading(spec, answer),
            ]
        )

    _print_padded(DETAIL_HEADER, rows)
    print(
        f"\n**Review load {result['load']:.2f} / 1.00**, the Adds column summed over "
        f"{len(weighted)} questions carrying {total:.2f} of weight.\n"
        f"Size floor for {pull['changed_files']} files and {result['lines']} lines: "
        f"{_size_floor(pull['changed_files'], result['lines'])}. "
        f"Tier: **{result['tier']}**"
        f"{', set by size rather than by load' if result['raised_by_size'] else ''}.\n"
        f"Jev read the patches of {result['coverage']:.0%} of the changed files; the rest "
        "reached it as paths and line counts.\n"
        "Weights live in questions.yml, so raise the one you care about and re-run."
    )


def _cost_usd(
    results: list[dict],
    settings: dict,
) -> float:
    """Price a whole run from its input tokens.

    TypeSafe bills input tokens only, at the rate questions.yml records.

    Args:
        results: Triage results.
        settings: Settings from questions.yml.

    Returns:
        The cost in dollars.
    """
    tokens = sum(result["response"].usage.input_tokens for result in results)
    return tokens * settings["input_usd_per_million"] / 1_000_000


def _print_totals(
    results: list[dict],
    specs: dict,
    settings: dict,
) -> None:
    """Print what the run cost and how long it took.

    Args:
        results: Triage results.
        specs: Question entries from questions.yml, keyed by id.
        settings: Settings from questions.yml.
    """
    tokens = sum(result["response"].usage.input_tokens for result in results)
    latency = sum(result["latency_ms"] for result in results)
    calls = len(results)
    thin = sum(1 for result in results if result["coverage"] < LOW_COVERAGE_BELOW)
    print(
        f"\n{_plural(calls, 'pull request')}, {len(specs)} questions each, one call apiece. "
        f"{tokens:,} input tokens, {latency:,} ms total, "
        f"{round(latency / calls):,} ms per call on average, "
        f"${_cost_usd(results, settings):.5f} at ${settings['input_usd_per_million']} "
        "per million input tokens."
    )
    if thin:
        print(
            f"{thin} of {calls} had patches too large to send whole, so Jev read part of the "
            "diff and the paths of the rest. Those are the ones the size floor guards."
        )


def _plural(
    count: int,
    noun: str,
) -> str:
    """Count a noun, adding an s only when there is more than one of them.

    Args:
        count: How many.
        noun: The singular form.

    Returns:
        The count and the noun, such as "1 pull request" or "18 pull requests".
    """
    return f"{count} {noun}" if count == 1 else f"{count} {noun}s"


def _report_path(
    repo: str,
    selector: dict,
) -> pathlib.Path:
    """Name the triage report after the repository and the selector.

    Args:
        repo: The repository as `owner/repo`.
        selector: The selector recorded in the dataset.

    Returns:
        The path to write, inside REPORT_DIR.
    """
    REPORT_DIR.mkdir(exist_ok=True)
    slug = re.sub(r"[^a-z0-9]+", "-", repo.lower()).strip("-")
    # A pull request names itself, so the state adds nothing and the report ends up
    # beside the dataset under the same name.
    if selector.get("pull") is not None:
        return REPORT_DIR / f"{slug}-pull-{selector['pull']}-triage.json"
    if selector.get("since"):
        tail = f"since-{selector['since']}"
    elif selector.get("limit") is None:
        tail = "all"
    else:
        tail = f"latest-{selector['limit']}"
    return REPORT_DIR / f"{slug}-{selector.get('state', 'open')}-{tail}-triage.json"


def _write_report(
    repo: str,
    selector: dict,
    results: list[dict],
    specs: dict,
    settings: dict,
) -> pathlib.Path:
    """Write one JSON report: every answer as Jev sent it, plus the judgment.

    Args:
        repo: The repository as `owner/repo`.
        selector: The selector recorded in the dataset.
        results: Triage results.
        specs: Question entries from questions.yml, keyed by id.
        settings: Settings from questions.yml.

    Returns:
        The path written.
    """
    weighted = _weighted(specs)
    total = sum(spec["weight"] for spec in weighted.values())
    counts: dict[str, int] = {tier: 0 for tier in TIERS}
    for result in results:
        counts[result["tier"]] += 1

    report = {
        "repo": repo,
        "selector": selector,
        "triaged_at": datetime.datetime.now(datetime.UTC).isoformat(timespec="seconds"),
        "model": settings["model"],
        "questions_asked": len(specs),
        "tier_counts": counts,
        "route_counts": {route: sum(1 for r in results if r["route"] == route) for route in ROUTES},
        "cost_usd": round(_cost_usd(results, settings), 8),
        "pull_requests": [
            {
                "number": result["pull"]["number"],
                "title": result["pull"]["title"],
                "url": result["pull"]["url"],
                "author": result["pull"]["author"],
                "changed_files": result["pull"]["changed_files"],
                "additions": result["pull"]["additions"],
                "deletions": result["pull"]["deletions"],
                "review_load": round(result["load"], 4),
                "consequence": round(result["consequence"], 4),
                "consequence_from": result["consequence_label"],
                "route": result["route"],
                "route_decided_by": result["route_decided_by"],
                "downgrade": result["downgrade"],
                "tier": result["tier"],
                "size_floor": _size_floor(result["pull"]["changed_files"], result["lines"]),
                "raised_by_size": result["raised_by_size"],
                "diff_coverage": round(result["coverage"], 4),
                "drivers": result["drivers"],
                "usage": result["response"].usage.model_dump(mode="json"),
                "latency_ms": result["latency_ms"],
                "questions": {
                    qid: {
                        "label": spec["label"],
                        "type": spec["type"],
                        "weight": spec.get("weight"),
                        "inverted": bool(spec.get("invert")),
                        "returned": result["answers"][qid].model_dump(mode="json"),
                        "credit": round(_credit(spec, result["answers"][qid]), 4)
                        if spec.get("weight")
                        else None,
                        "adds_to_load": round(_contribution(spec, result["answers"][qid], total), 4)
                        if spec.get("weight")
                        else None,
                    }
                    for qid, spec in specs.items()
                },
            }
            for result in sorted(results, key=lambda item: item["load"], reverse=True)
        ],
    }

    path = _report_path(repo, selector)
    path.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    return path


def _markdown_lines(
    repo: str,
    results: list[dict],
    specs: dict,
    settings: dict,
) -> list[str]:
    """Build the markdown report: the same table and groups the run printed.

    Args:
        repo: The repository as `owner/repo`.
        results: Triage results.
        specs: Question entries from questions.yml, keyed by id.
        settings: Settings from questions.yml.

    Returns:
        Markdown lines, without trailing newlines.
    """
    ordered = sorted(results, key=lambda item: item["load"], reverse=True)
    tokens = sum(result["response"].usage.input_tokens for result in results)
    stamp = datetime.datetime.now(datetime.UTC).strftime("%d %B %Y")

    lines = [
        f"# Triage: {repo}",
        "",
        (
            f"{len(results)} pull requests, {len(specs)} questions each, one call apiece, "
            f"on {stamp} with `{settings['model']}`."
        ),
        (
            f"{tokens:,} input tokens, ${_cost_usd(results, settings):.5f} at "
            f"${settings['input_usd_per_million']} per million."
        ),
        "",
        *_padded_lines(TRIAGE_HEADER, [_triage_row(result) for result in ordered]),
        "",
        (
            "Load is the weighted average of nine questions, 0 to 1. Tier comes from that "
            f"load, raised when size demands it: over {SIZE_FLOORS[0][0]} files or "
            f"{SIZE_FLOORS[0][1]:,} lines is high whatever Jev returned."
        ),
    ]
    return lines + _markdown_groups(results)


def _markdown_groups(results: list[dict]) -> list[str]:
    """Build one markdown section per tier, heaviest tier first.

    Args:
        results: Triage results.

    Returns:
        Markdown lines, without trailing newlines.
    """
    lines: list[str] = []
    for tier in reversed(TIERS):
        members = sorted(
            (result for result in results if result["tier"] == tier),
            key=lambda item: item["load"],
            reverse=True,
        )
        if not members:
            continue

        lines += ["", f"## {tier} ({len(members)})", "", TIER_ADVICE[tier], ""]
        for result in members:
            pull = result["pull"]
            drivers = ", ".join(result["drivers"]) or "nothing above the driver floor"
            size_note = ", raised by the size floor" if result["raised_by_size"] else ""
            lines.append(
                f"- [#{pull['number']}]({pull['url']}) load {result['load']:.2f}{size_note}: "
                f"{pull['title']}"
            )
            lines.append(f"  - drivers: {drivers}")
            if result["coverage"] < LOW_COVERAGE_BELOW:
                lines.append(
                    f"  - read from {result['coverage']:.0%} of the changed files, so the load "
                    "is a read on part of the diff"
                )
    return lines


def _write_markdown(
    repo: str,
    selector: dict,
    results: list[dict],
    specs: dict,
    settings: dict,
) -> pathlib.Path:
    """Write the markdown report beside the JSON one, same stem.

    Args:
        repo: The repository as `owner/repo`.
        selector: The selector recorded in the dataset.
        results: Triage results.
        specs: Question entries from questions.yml, keyed by id.
        settings: Settings from questions.yml.

    Returns:
        The path written.
    """
    path = _report_path(repo, selector).with_suffix(".md")
    path.write_text("\n".join(_markdown_lines(repo, results, specs, settings)) + "\n", "utf-8")
    return path


def _load_dataset(path: pathlib.Path) -> dict:
    """Read a dataset written by fetch_prs.py.

    Args:
        path: The dataset file.

    Returns:
        The parsed dataset.

    Raises:
        SystemExit: If the file is missing or holds no pull requests.
    """
    if not path.exists():
        sys.exit(f"no dataset at {path}\n  build one: uv run fetch_prs.py owner/repo")

    dataset = json.loads(path.read_text(encoding="utf-8"))
    if not dataset.get("pull_requests"):
        sys.exit(f"{path} holds no pull requests")

    logger.info(
        f"Read {len(dataset['pull_requests'])} pull requests from {path}, "
        f"fetched {dataset.get('fetched_at', 'at an unrecorded time')}"
    )
    return dataset


def triage(
    dataset: dict,
    explain: int | None = None,
    verbose: bool = False,
) -> None:
    """Triage every pull request in a dataset and print the result.

    Args:
        dataset: A dataset as written by fetch_prs.py.
        explain: A pull request number to print every question for, or None.
        verbose: Print the raw answers for every pull request too.
    """
    settings, specs = _load_payload()
    _require_api_key()

    repo = dataset["repo"]
    pulls = dataset["pull_requests"]
    questions = {qid: _build_question(qid, spec) for qid, spec in specs.items()}
    client = TypeSafeClient()

    logger.info(f"Asking Jev {len(specs)} questions about each of {len(pulls)} pull requests")
    results = [_triage_one(client, repo, pull, questions, specs, settings) for pull in pulls]

    if verbose:
        for result in results:
            print(f"\nRaw response for #{result['pull']['number']}:")
            print(json.dumps(result["response"].model_dump(mode="json"), indent=2))

    print(f"\n## Triage: {repo}, {_plural(len(results), 'pull request')}\n")
    _print_triage_table(results)
    print(
        "\nLoad is the weighted average of nine questions, 0 to 1. Tier comes from that load, "
        f"raised when size demands it: over {SIZE_FLOORS[-1][0]} files or "
        f"{SIZE_FLOORS[-1][1]} lines cannot be trivial, over {SIZE_FLOORS[0][0]} files or "
        f"{SIZE_FLOORS[0][1]} lines is high whatever Jev returned."
    )
    _print_groups(results)

    if explain is not None:
        wanted = [result for result in results if result["pull"]["number"] == explain]
        if not wanted:
            print(f"\n#{explain} is not in this dataset, so there is nothing to explain.")
        else:
            _print_detail(wanted[0], specs)

    _print_totals(results, specs, settings)
    selector = dataset.get("selector", {})
    print(f"Report: {_write_report(repo, selector, results, specs, settings)}")
    print(f"Markdown: {_write_markdown(repo, selector, results, specs, settings)}")


def main() -> None:
    """Parse arguments, get a dataset, and run the triage."""
    parser = argparse.ArgumentParser(
        description="Triage a repository's pull requests, one Jev call each.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Example usage:
    # The 10 most recent open pull requests, fetched and triaged
    uv run pr_triage.py agentic-community/mcp-gateway-registry

    # Every open one, or everything opened in the last 30 days
    uv run pr_triage.py owner/repo --all
    uv run pr_triage.py owner/repo --since 30

    # Replay a dataset already on disk, with no GitHub calls
    uv run pr_triage.py --dataset data/owner-repo-open-all.json

    # Print every question and what it contributed for one pull request
    uv run pr_triage.py --dataset data/owner-repo-open-all.json --explain 1693

The questions, the model pin and the state budgets live in questions.yml. The
tiers, the size floor and the advice per tier live in pr_triage.py.

Reads TYPESAFE_API_KEY from the environment, or from .env beside this file or at
the repo root. Fetching also reads GITHUB_TOKEN, GH_TOKEN, or `gh auth token`.
""",
    )
    parser.add_argument(
        "repo",
        nargs="?",
        help="Repository as owner/repo, or a GitHub URL. Omit it when passing --dataset",
    )
    parser.add_argument(
        "--dataset",
        type=pathlib.Path,
        help="Triage a dataset already on disk instead of fetching one",
    )
    parser.add_argument(
        "--limit",
        type=int,
        default=10,
        help="How many of the most recent to fetch (default: 10)",
    )
    parser.add_argument(
        "--all",
        action="store_true",
        help="Fetch every pull request the selector allows, ignoring --limit",
    )
    parser.add_argument(
        "--since",
        help="Fetch pull requests opened on or after YYYY-MM-DD, or in the last N days",
    )
    parser.add_argument(
        "--state",
        default="open",
        choices=("open", "closed", "all"),
        help="Which pull requests to fetch (default: open)",
    )
    parser.add_argument(
        "--explain",
        type=int,
        help="Print every question and what it contributed, for one pull request number",
    )
    parser.add_argument(
        "--debug",
        action="store_true",
        help="Turn on debug logging",
    )
    parser.add_argument(
        "--verbose",
        action="store_true",
        help="Pretty print the raw JSON Jev returned for every pull request",
    )
    args = parser.parse_args()

    if args.debug:
        logging.getLogger().setLevel(logging.DEBUG)

    if args.dataset:
        dataset = _load_dataset(args.dataset)
    elif args.repo:
        # A pull request URL carries its own number, and that number turns the run
        # into one pull request: no listing, and --limit, --since and --state have
        # nothing to select from.
        repo, pull = parse_target(args.repo)
        path = build_dataset(
            repo=repo,
            state=args.state,
            limit=None if args.all else args.limit,
            since=parse_since(args.since),
            pull=pull,
        )
        dataset = _load_dataset(path)
    else:
        parser.error("pass a repo to fetch, or --dataset to replay one")

    triage(dataset, args.explain, args.verbose)


if __name__ == "__main__":
    main()
