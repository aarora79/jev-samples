"""The evaluation harness behind next_watch.py's default command.

It holds the hold-out split, the sampled negatives, the per-member scoring, the
metric table and its notes, the explain view, and the reports. It has no command
line of its own: `uv run next_watch.py` runs it.
"""

import datetime
import json
import logging
import pathlib
import random
import sys

from typesafe_sdk import (
    TypeSafeClient,
)

from next_watch import (
    LIKED_FLOOR,
    MIN_LIKED_HISTORY,
    USUAL_GENRES,
    ask_jev,
    build_questions,
    build_shortlist,
    build_state,
    genre_profile,
    liked,
    load_catalog,
    load_payload,
    option_text,
    padded_lines,
    ranker_scores,
    ratings_before,
    require_api_key,
    score_candidate,
    usual_genres,
    widens,
)

# Configure logging with basicConfig
logging.basicConfig(
    level=logging.INFO,  # Set the log level to INFO
    # Define log message format
    format="%(asctime)s,p%(process)s,{%(filename)s:%(lineno)d},%(levelname)s,%(message)s",
)
logger = logging.getLogger(__name__)

# Every evaluation writes one JSON report and one markdown report here.
REPORT_DIR: pathlib.Path = pathlib.Path(__file__).parent / "data"

# Shortlist scores in the report keep this many decimal places.
REPORT_DECIMALS: int = 4

# Hit rate is reported at these cutoffs.
HIT_AT: tuple[int, ...] = (1, 5)

# The JSON report keeps this many members in full, chosen to span the headline
# ranker's reciprocal rank from best to worst. Every other ranked member keeps
# one line in the reciprocal-rank table, which is all the metrics rest on.
REPORT_EXAMPLES: int = 5

# The paired bootstrap measures the headline Jev ranker against this free one,
# the ranker that reads the same genres Jev sees.
BOOTSTRAP_BASELINE: str = "genre match"

# Resamples, seed and tail for the bootstrap. 0.025 on each side gives a 95%
# interval, and the fixed seed lets anyone recompute it from a saved report.
BOOTSTRAP_RESAMPLES: int = 10_000
BOOTSTRAP_SEED: int = 0
BOOTSTRAP_TAIL: float = 0.025

# Two reciprocal ranks closer than this count as a tie when the bootstrap counts
# the members a ranker did better and worse for.
BOOTSTRAP_TOLERANCE: float = 1e-9


def _sampled_negatives(
    excluded: set[int],
    before: int,
    movies: dict[int, dict],
    index: dict[int, list[int]],
    count: int,
    rng: random.Random,
) -> list[int]:
    """Draw titles the member never rated, each in proportion to its popularity.

    The evaluation needs negatives that no ranker chose. Taking the shortlist's
    own top titles would stack the list against the hidden one on exactly the
    features the free rankers sort by, and every one of them would score below
    random. Sampling by popularity is the protocol SASRec and BERT4Rec report
    against, and it keeps obscure titles from making the answer easy to spot.

    The draw is without replacement, using a key of u ** (1 / weight) per title
    and keeping the largest (Efraimidis and Spirakis, 2006).

    Args:
        excluded: Titles the member has rated.
        before: The moment of recommendation, for popularity.
        movies: Title records keyed by id.
        index: Sorted rating times keyed by movie id.
        count: How many to draw.
        rng: The seeded generator for this member.

    Returns:
        Movie ids, in no particular order.
    """
    keyed = []
    for movie_id in sorted(movies):
        popularity = ratings_before(index, movie_id, before)
        if movie_id in excluded or popularity == 0:
            continue
        keyed.append((rng.random() ** (1 / popularity), movie_id))
    keyed.sort(reverse=True)
    return [movie_id for _, movie_id in keyed[:count]]


def _expected_hits(
    scores: dict[int, float],
    target: int,
) -> dict:
    """Reciprocal rank and hit rates for the held-out title, averaged over ties.

    Genre match gives every title with the same genres the same score, and Jev
    often returns 0.0 for more than one option. Breaking those ties by list order
    would hand a ranker credit for where the shuffle put the answer, so this
    averages over every order the tie allows.

    Args:
        scores: One ranker's scores keyed by movie id.
        target: The held-out title.

    Returns:
        Reciprocal rank under `rr` and each hit rate under `hit@k`.
    """
    value = scores[target]
    better = sum(1 for score in scores.values() if score > value)
    tied = sum(1 for score in scores.values() if score == value)
    ranks = range(better + 1, better + tied + 1)
    result = {"rr": sum(1 / rank for rank in ranks) / tied}
    for cutoff in HIT_AT:
        result[f"hit@{cutoff}"] = sum(1 for rank in ranks if rank <= cutoff) / tied
    return result


def _top_pick(
    scores: dict[int, float],
    order: list[int],
) -> int:
    """Name a ranker's first title, breaking ties by the shuffled list order.

    Args:
        scores: One ranker's scores keyed by movie id.
        order: Movie ids in the shuffled order Jev saw them.

    Returns:
        The movie id ranked first.
    """
    return max(order, key=lambda movie_id: (scores[movie_id], -order.index(movie_id)))


def _split(timeline: list[tuple[int, int, float]]) -> tuple[list, tuple] | None:
    """Hold out a member's last like and keep what came strictly before it.

    Ratings made in the same second as the held-out one stay out of the history,
    because MovieLens members often rate in batches and a batch says nothing
    about order.

    Args:
        timeline: The member's ratings, oldest first.

    Returns:
        Tuple of (history, held-out entry), or None when the member never liked
        anything.
    """
    likes = liked(timeline)
    if not likes:
        return None
    target = likes[-1]
    history = [entry for entry in timeline if entry[0] < target[0]]
    return history, target


def _evaluate_member(
    user_id: int,
    timeline: list[tuple[int, int, float]],
    context: dict,
) -> dict:
    """Hide one member's last like, shortlist around it, and score every ranker.

    Args:
        user_id: The member.
        timeline: The member's ratings, oldest first.
        context: Movies, index, settings, specs, client and seed for the run.

    Returns:
        One result: the outcome, and for ranked members the shortlist, each
        ranker's metrics and top pick, and the call's cost.
    """
    split = _split(timeline)
    if split is None:
        return {"user": user_id, "outcome": "no-like"}
    history, (before, target, _) = split
    if len(liked(history)) < MIN_LIKED_HISTORY:
        return {"user": user_id, "outcome": "cold-start", "likes": len(liked(history))}

    movies, index, settings = context["movies"], context["index"], context["settings"]
    profile = genre_profile(history, movies)
    rated = {movie_id for _, movie_id, _ in timeline}
    size = settings["shortlist_size"]
    # Retrieval and ranking get separate numbers. `retrieved` says whether the
    # free shortlist would have found the hidden title on its own; the ranking
    # metrics then score every ranker on the same fair candidate set.
    natural = build_shortlist(profile, rated - {target}, before, movies, index, size)
    retrieved = any(c["id"] == target for c in natural)
    rng = random.Random(f"{context['seed']}-{user_id}")  # nosec B311 - seeded for reproducibility, not security
    negatives = _sampled_negatives(rated, before, movies, index, size - 1, rng)
    shortlist = [
        score_candidate(movie_id, profile, before, movies, index)
        for movie_id in negatives + [target]
    ]
    rng.shuffle(shortlist)

    response, latency_ms = None, 0
    if context["client"] is not None:
        state = build_state(history, profile, movies, settings)
        questions = build_questions(context["specs"], shortlist, movies)
        response, latency_ms = ask_jev(context["client"], state, questions, settings)
        logger.info(f"member {user_id}: {latency_ms} ms, {response.usage.input_tokens} tokens")
    answers = response.answers if response else None
    return _score_member(user_id, target, shortlist, retrieved, profile, answers, context) | {
        "history": history,
        "response": response,
        "latency_ms": latency_ms,
    }


def _score_member(
    user_id: int,
    target: int,
    shortlist: list[dict],
    retrieved: bool,
    profile: dict[str, float],
    answers: dict | None,
    context: dict,
) -> dict:
    """Turn one member's scores into metrics and top picks.

    Args:
        user_id: The member.
        target: The held-out title.
        shortlist: The candidates, in the order Jev saw them.
        retrieved: Whether the free shortlist found the title on its own.
        profile: The member's liked-genre counts.
        answers: Jev's answers, or None when no call was made.
        context: Movies and specs for the run.

    Returns:
        The ranked part of one result.
    """
    movies = context["movies"]
    usual = usual_genres(profile)
    order = [c["id"] for c in shortlist]
    scores = ranker_scores(shortlist, answers, context["specs"])
    rankers = {}
    for name, ranker in scores.items():
        pick = _top_pick(ranker, order)
        rankers[name] = _expected_hits(ranker, target) | {
            "top_pick": pick,
            "widens": widens(movies[pick], usual),
        }
    return {
        "user": user_id,
        "outcome": "ranked",
        "target": target,
        "retrieved": retrieved,
        "shortlist": shortlist,
        "usual_genres": sorted(usual),
        "scores": scores,
        "rankers": rankers,
    }


def _metric_rows(ranked: list[dict]) -> list[list[str]]:
    """Average each ranker's metrics over every ranked member.

    Args:
        ranked: Results whose outcome is "ranked".

    Returns:
        One table row per ranker, plus the random baseline, which is arithmetic.
    """
    size = len(ranked[0]["shortlist"])
    harmonic = sum(1 / rank for rank in range(1, size + 1))
    rows = [
        ["random (expected)", f"{harmonic / size:.3f}"]
        + [f"{min(cutoff, size) / size:.3f}" for cutoff in HIT_AT]
        + ["n/a", "n/a"]
    ]
    for name in ranked[0]["rankers"]:
        values = [result["rankers"][name] for result in ranked]
        picks = {value["top_pick"] for value in values}
        row = [name, f"{sum(v['rr'] for v in values) / len(values):.3f}"]
        row += [f"{sum(v[f'hit@{c}'] for v in values) / len(values):.3f}" for c in HIT_AT]
        row += [f"{sum(v['widens'] for v in values) / len(values):.3f}", str(len(picks))]
        rows.append(row)
    return rows


def _metric_header() -> list[str]:
    """Name the metric table's columns.

    Returns:
        Header cells matching _metric_rows.
    """
    return ["Ranker", "MRR"] + [f"Hit@{c}" for c in HIT_AT] + ["Widens", "Distinct #1"]


def _plural(
    count: int,
    noun: str,
) -> str:
    """Count a noun, adding an s only when there is more than one of them.

    Args:
        count: How many.
        noun: The singular form.

    Returns:
        The count and the noun, such as "1 member" or "12 members".
    """
    return f"{count} {noun}" if count == 1 else f"{count} {noun}s"


def _metric_notes(
    ranked: list[dict],
    counts: dict[str, int],
) -> list[str]:
    """Explain every number the metric table prints.

    Args:
        ranked: Results whose outcome is "ranked".
        counts: How many members landed in each outcome.

    Returns:
        Explanation lines.
    """
    size = len(ranked[0]["shortlist"])
    found = sum(1 for result in ranked if result["retrieved"])
    return [
        (
            f"Each ranker ordered the same {size} titles per member: the title the member "
            f"liked last, which was hidden, and {size - 1} titles they never rated, drawn "
            "in proportion to how often everyone else had rated them by then."
        ),
        (
            "MRR is the mean of 1 / (the rank that title landed at), so 1.0 means first every "
            f"time and {1 / size:.3f} means last every time. Hit@k is the share of members "
            "whose title landed in the top k. Ties count as the average over every order "
            "the tie allows."
        ),
        (
            "Widens is the share of members whose #1 pick carries none of their "
            f"{USUAL_GENRES} most-liked genres. Distinct #1 counts different #1 picks "
            f"across all {len(ranked)} members, so a ranker that shows everyone the same "
            "blockbuster scores 1."
        ),
        (
            f"Retrieval is its own number: the free shortlist of {size} would have found "
            f"the hidden title for {found} of {len(ranked)} members. A ranker can only "
            "reorder what retrieval hands it."
        ),
        (
            f"Rules settled {_plural(counts.get('cold-start', 0), 'member')} at no cost: "
            f"fewer than {MIN_LIKED_HISTORY} likes before the hidden one, so popularity "
            f"serves them. {_plural(counts.get('no-like', 0), 'member')} never rated anything "
            f"{LIKED_FLOOR:g} stars or higher, so they hold nothing to hide."
        ),
    ]


def _cost_usd(
    ranked: list[dict],
    settings: dict,
) -> tuple[int, float]:
    """Price a run from its input tokens.

    Args:
        ranked: Results whose outcome is "ranked".
        settings: Settings from questions.yml.

    Returns:
        Tuple of (input tokens, dollars).
    """
    tokens = sum(r["response"].usage.input_tokens for r in ranked if r["response"])
    return tokens, tokens * settings["input_usd_per_million"] / 1_000_000


def _cost_line(
    ranked: list[dict],
    specs: dict,
    settings: dict,
) -> str:
    """Build the one line that says what the Jev calls cost and how long they took.

    Args:
        ranked: Results whose outcome is "ranked".
        specs: Question entries from questions.yml.
        settings: Settings from questions.yml.

    Returns:
        The line, or a note that no call was made.
    """
    calls = sum(1 for result in ranked if result["response"])
    if not calls:
        return "No Jev calls: the free rankers only."
    tokens, dollars = _cost_usd(ranked, settings)
    latency = sum(result["latency_ms"] for result in ranked)
    return (
        f"{calls} Jev calls, {len(specs)} questions each. {tokens:,} input tokens, "
        f"{round(latency / calls):,} ms per call on average, ${dollars:.5f} at "
        f"${settings['input_usd_per_million']} per million input tokens, "
        f"${dollars / calls:.6f} per member."
    )


def _explain_lines(
    result: dict,
    movies: dict[int, dict],
    settings: dict,
) -> list[str]:
    """Show one member's state and every shortlisted title under every ranker.

    Args:
        result: One ranked result.
        movies: Title records keyed by id.
        settings: Settings from questions.yml.

    Returns:
        Lines to print.
    """
    profile = genre_profile(result["history"], movies)
    state = build_state(result["history"], profile, movies, settings)
    lines = [f"\n### Member {result['user']}\n"]
    lines += [f"{field}:\n{text}\n" for field, text in state.items()]
    names = list(result["scores"])
    rows = []
    for c in sorted(result["shortlist"], key=lambda c: -result["scores"][names[-1]][c["id"]]):
        marker = "  <- hidden like" if c["id"] == result["target"] else ""
        cells = [option_text(movies[c["id"]])[:60] + marker]
        values = [result["scores"][name][c["id"]] for name in names]
        # Popularity is a count, and every other column is a score from 0 up.
        rows.append(
            cells + [f"{v:.0f}" if n == "popularity" else f"{v:.3f}" for n, v in zip(names, values)]
        )
    lines += padded_lines(["Title"] + names, rows)
    lines.append(f"\nSorted by {names[-1]}. Usual genres: {', '.join(result['usual_genres'])}.")
    return lines


def _explain_or_why(
    user_id: int,
    results: list[dict],
    movies: dict[int, dict],
    settings: dict,
) -> list[str]:
    """Explain one member in full, or say why there is nothing to explain.

    Args:
        user_id: The member `--explain` named.
        results: Every result, ranked or not.
        movies: Title records keyed by id.
        settings: Settings from questions.yml.

    Returns:
        Lines to print.
    """
    wanted = [result for result in results if result["user"] == user_id]
    if not wanted:
        return [f"\nMember {user_id} is not in MovieLens small, so there is nothing to explain."]
    result = wanted[0]
    if result["outcome"] == "cold-start":
        return [
            (
                f"\nMember {user_id} had {result['likes']} likes before the hidden one, under "
                f"the {MIN_LIKED_HISTORY} the rules ask for, so popularity served them without "
                "a call."
            )
        ]
    if result["outcome"] == "no-like":
        return [f"\nMember {user_id} never rated anything {LIKED_FLOOR:g} stars or higher."]
    return _explain_lines(result, movies, settings)


def _report_stem(
    users: int,
    seed: int,
    jev: bool,
) -> str:
    """Name the report files after the sample size, the seed and the rankers.

    Args:
        users: How many members were sampled.
        seed: The sampling seed.
        jev: Whether Jev ranked as well.

    Returns:
        A file stem without an extension.
    """
    kind = "jev" if jev else "baselines"
    return f"movielens-small-{users}-members-seed-{seed}-{kind}"


def _rounded(candidate: dict) -> dict:
    """Round a shortlist entry's scores to four places for the report.

    The ranking uses full precision; the report only needs enough to read.

    Args:
        candidate: One shortlist entry.

    Returns:
        A copy with float values rounded.
    """
    return {
        key: round(value, REPORT_DECIMALS) if isinstance(value, float) else value
        for key, value in candidate.items()
    }


def _report_rows(results: list[dict]) -> list[dict]:
    """Turn every result into the row the JSON report stores.

    Per-ranker scores stay out: the free ones follow from each shortlist entry,
    and the Jev ones are the probabilities under `jev`.

    Args:
        results: Every result, ranked or not.

    Returns:
        One row per result, with the shortlist rounded and Jev's answer as JSON.
    """
    skipped = ("history", "response", "scores")
    rows = []
    for result in results:
        row = {key: value for key, value in result.items() if key not in skipped}
        if "shortlist" in row:
            row["shortlist"] = [_rounded(candidate) for candidate in row["shortlist"]]
        if result.get("response"):
            row["jev"] = result["response"].model_dump(mode="json")
        rows.append(row)
    return rows


def _headline_ranker(names: list[str]) -> str:
    """Name the ranker the report leads with: the first Jev one, else the baseline.

    Args:
        names: Ranker names in table order.

    Returns:
        `jev next_watch` for the shipped questions.yml, or BOOTSTRAP_BASELINE
        when Jev did not rank.
    """
    return next((name for name in names if name.startswith("jev ")), BOOTSTRAP_BASELINE)


def _reciprocal_ranks(rows: list[dict]) -> dict[str, dict[str, float]]:
    """Collect every ranked member's reciprocal rank under every ranker.

    Args:
        rows: Every report row, in run order.

    Returns:
        Reciprocal ranks keyed by member id as text, then by ranker, rounded to
        REPORT_DECIMALS places, in run order.
    """
    return {
        str(row["user"]): {
            name: round(values["rr"], REPORT_DECIMALS) for name, values in row["rankers"].items()
        }
        for row in rows
        if row["outcome"] == "ranked"
    }


def _paired_bootstrap(
    ranks: dict[str, dict[str, float]],
    ranker: str,
    baseline: str,
) -> dict:
    """Resample members to put an interval on one ranker's MRR lead over another.

    Each resample draws as many members as ranked, with replacement, and
    averages the per-member difference in reciprocal rank. The interval is the
    middle 95% of those means.

    Args:
        ranks: The reciprocal-rank table from _reciprocal_ranks, in run order.
        ranker: The ranker to measure.
        baseline: The ranker to measure it against.

    Returns:
        The difference in MRR, its 95% interval, and how many members the ranker
        did better and worse for.
    """
    diff = [row[ranker] - row[baseline] for row in ranks.values()]
    n = len(diff)
    rng = random.Random(BOOTSTRAP_SEED)  # nosec B311 - seeded for reproducibility, not security
    means = sorted(
        sum(diff[rng.randrange(n)] for _ in range(n)) / n for _ in range(BOOTSTRAP_RESAMPLES)
    )
    low = means[round(BOOTSTRAP_TAIL * BOOTSTRAP_RESAMPLES)]
    high = means[round((1 - BOOTSTRAP_TAIL) * BOOTSTRAP_RESAMPLES)]
    return {
        "ranker": ranker,
        "baseline": baseline,
        "members": n,
        "resamples": BOOTSTRAP_RESAMPLES,
        "seed": BOOTSTRAP_SEED,
        "mrr_difference": round(sum(diff) / n, REPORT_DECIMALS),
        "interval_95": [round(low, REPORT_DECIMALS), round(high, REPORT_DECIMALS)],
        "better": sum(1 for value in diff if value > BOOTSTRAP_TOLERANCE),
        "worse": sum(1 for value in diff if value < -BOOTSTRAP_TOLERANCE),
    }


def _bootstrap_line(bootstrap: dict) -> str:
    """Say what the paired bootstrap found, in one line.

    Args:
        bootstrap: The dict _paired_bootstrap returns.

    Returns:
        The line the evaluation prints and `--bootstrap` reprints.
    """
    low, high = bootstrap["interval_95"]
    return (
        f"Paired bootstrap of {bootstrap['ranker']} against {bootstrap['baseline']} over "
        f"{_plural(bootstrap['members'], 'member')}, {bootstrap['resamples']:,} resamples: "
        f"MRR difference {bootstrap['mrr_difference']:+.3f}, 95% interval {low:+.3f} to "
        f"{high:+.3f}. {bootstrap['ranker']} ranked the hidden title higher for "
        f"{_plural(bootstrap['better'], 'member')} and lower for {bootstrap['worse']}."
    )


def _example_results(rows: list[dict]) -> list[dict]:
    """Pick the members the JSON report keeps in full.

    The rule: sort ranked members by the headline ranker's reciprocal rank, best
    first, ties broken by member id, and take REPORT_EXAMPLES positions at even
    steps from the first to the last.

    Args:
        rows: Every report row, ranked or not.

    Returns:
        The chosen rows, best rank first, or an empty list when nobody ranked.
    """
    ranked = [row for row in rows if row["outcome"] == "ranked"]
    if not ranked:
        return []
    headline = _headline_ranker(list(ranked[0]["rankers"]))
    order = sorted(ranked, key=lambda row: (-row["rankers"][headline]["rr"], row["user"]))
    k = min(REPORT_EXAMPLES, len(order))
    if k == 1:
        return order[:1]
    return [order[round(i * (len(order) - 1) / (k - 1))] for i in range(k)]


def _trim_report(report: dict) -> dict:
    """Cut a full report down to the shape the repo commits.

    Five members in full show what an answer looks like. The reciprocal-rank
    table keeps every number the metric table and the bootstrap rest on, so
    anyone can check both from the file alone.

    Args:
        report: The full report: evaluated_at, model, dataset, metrics, and one
            row per sampled member under `results`.

    Returns:
        The trimmed report, with results_shown and results_total saying how
        much of the run the full entries cover.
    """
    ranks = _reciprocal_ranks(report["results"])
    headline = _headline_ranker(list(next(iter(ranks.values()), {})))
    bootstrap = None
    if ranks and headline != BOOTSTRAP_BASELINE:
        bootstrap = _paired_bootstrap(ranks, headline, BOOTSTRAP_BASELINE)
    examples = _example_results(report["results"])
    return {
        "evaluated_at": report["evaluated_at"],
        "model": report["model"],
        "dataset": report["dataset"],
        "metrics": report["metrics"],
        "bootstrap": bootstrap,
        "results_shown": len(examples),
        "results_total": len(report["results"]),
        "results": examples,
        "reciprocal_ranks": ranks,
    }


def _report_text(report: dict) -> str:
    """Serialize a report the one way both a live run and a regeneration write it.

    Args:
        report: The trimmed report.

    Returns:
        Indented JSON with a trailing newline.
    """
    return json.dumps(report, indent=2, default=str) + "\n"


def _write_reports(
    stem: str,
    report: dict,
    lines: list[str],
) -> tuple[pathlib.Path, pathlib.Path]:
    """Write the trimmed report as JSON, and the printed tables as markdown.

    Args:
        stem: The file stem.
        report: The trimmed report from _trim_report.
        lines: The markdown already printed.

    Returns:
        Tuple of (JSON path, markdown path).
    """
    REPORT_DIR.mkdir(exist_ok=True)
    json_path = REPORT_DIR / f"{stem}.json"
    json_path.write_text(_report_text(report), encoding="utf-8")
    md_path = REPORT_DIR / f"{stem}.md"
    md_path.write_text("\n".join(lines) + "\n", encoding="utf-8")
    return json_path, md_path


def _summary_lines(
    results: list[dict],
    ranked: list[dict],
    seed: int,
    report: dict,
    context: dict,
) -> list[str]:
    """Build the metric table, its notes, the cost line and the bootstrap line.

    Args:
        results: Every result, ranked or not.
        ranked: Results whose outcome is "ranked".
        seed: The sampling seed.
        report: The trimmed report, which holds the bootstrap.
        context: Settings and specs for the run.

    Returns:
        Lines to print and to write as markdown.
    """
    counts: dict[str, int] = {}
    for result in results:
        counts[result["outcome"]] = counts.get(result["outcome"], 0) + 1
    lines = [
        f"## next-watch: {len(ranked)} of {len(results)} sampled members ranked, seed {seed}\n",
        *padded_lines(_metric_header(), _metric_rows(ranked)),
        "",
        *_metric_notes(ranked, counts),
        "",
        _cost_line(ranked, context["specs"], context["settings"]),
    ]
    if report["bootstrap"] is not None:
        lines += ["", _bootstrap_line(report["bootstrap"])]
    return lines


def _report(
    results: list[dict],
    users: int,
    seed: int,
    explain: int | None,
    baselines_only: bool,
    verbose: bool,
    context: dict,
) -> None:
    """Print the metric table and its notes, then write both reports.

    Args:
        results: Every result, ranked or not.
        users: How many members were sampled.
        seed: The sampling seed.
        explain: A member id to print in full, or None.
        baselines_only: Whether Jev was skipped.
        verbose: Print the raw JSON Jev returned for every member.
        context: Movies, settings and specs for the run.
    """
    ranked = [result for result in results if result["outcome"] == "ranked"]
    if not ranked:
        sys.exit("No sampled member had enough history to rank. Try a larger --users.")

    if verbose:
        for result in ranked:
            if result["response"]:
                print(f"\nRaw response for member {result['user']}:")
                print(json.dumps(result["response"].model_dump(mode="json"), indent=2))

    report = _trim_report(
        {
            "evaluated_at": datetime.datetime.now(datetime.UTC).isoformat(timespec="seconds"),
            "model": context["settings"]["model"],
            "dataset": "MovieLens ml-latest-small (GroupLens, University of Minnesota)",
            "metrics": [dict(zip(_metric_header(), row)) for row in _metric_rows(ranked)],
            "results": _report_rows(results),
        }
    )
    lines = _summary_lines(results, ranked, seed, report, context)
    if explain is not None:
        lines += _explain_or_why(explain, results, context["movies"], context["settings"])
    print("\n".join(lines))
    # Name the files for how many members actually ran, so an --explain member
    # added to the sample writes a new report instead of overwriting a recorded one.
    stem = _report_stem(len(results), seed, not baselines_only)
    json_path, md_path = _write_reports(stem, report, lines)
    print(f"\nReport: {json_path}\nMarkdown: {md_path}")


def evaluate(
    users: int,
    seed: int,
    explain: int | None = None,
    baselines_only: bool = False,
    verbose: bool = False,
) -> None:
    """Evaluate every ranker on a seeded sample of members and print the tables.

    Args:
        users: How many members to sample.
        seed: The sampling and shuffling seed.
        explain: A member id to print in full, or None.
        baselines_only: Skip Jev and score the free rankers only.
        verbose: Print the raw JSON Jev returned for every member.
    """
    settings, specs = load_payload()
    if not baselines_only:
        require_api_key()
    movies, timelines, index = load_catalog()
    rng = random.Random(seed)  # nosec B311 - seeded for reproducibility, not security
    sample = sorted(rng.sample(sorted(timelines), min(users, len(timelines))))
    if explain is not None and explain in timelines and explain not in sample:
        sample.append(explain)
    context = {
        "movies": movies,
        "index": index,
        "settings": settings,
        "specs": specs,
        "seed": seed,
        "client": None if baselines_only else TypeSafeClient(),
    }
    results = [_evaluate_member(user_id, timelines[user_id], context) for user_id in sample]
    _report(results, users, seed, explain, baselines_only, verbose, context)


def replay_bootstrap(path: pathlib.Path) -> None:
    """Recompute the paired bootstrap from a saved JSON report and print it.

    Reads the reciprocal-rank table alone, so it needs no key, no catalog and no
    calls.

    Args:
        path: A JSON report an evaluation wrote.

    Raises:
        SystemExit: If the file is missing, holds no reciprocal-rank table, or
            comes from a run where Jev did not rank.
    """
    if not path.exists():
        sys.exit(f"{path}: no such report")
    report = json.loads(path.read_text(encoding="utf-8"))
    ranks = report.get("reciprocal_ranks")
    if not ranks:
        sys.exit(f"{path}: no reciprocal_ranks table, so there is nothing to resample")
    headline = _headline_ranker(list(next(iter(ranks.values()))))
    if headline == BOOTSTRAP_BASELINE:
        sys.exit(f"{path}: a baselines-only report, with no Jev ranker to measure")
    print(_bootstrap_line(_paired_bootstrap(ranks, headline, BOOTSTRAP_BASELINE)))
