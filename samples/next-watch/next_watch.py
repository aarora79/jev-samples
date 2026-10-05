"""Rank what a MovieLens member watches next, one Jev call each.

The shape follows GenRec, the LLM-backed ranker Netflix described in July 2026:
write the member's history out as text, read it once, and score every candidate
title in the same pass. Jev's Choice does that pass. Rules do the rest for free:
they build a shortlist of twenty from genre match and popularity, and they serve
members with too little history a popularity list without calling Jev at all.

Two modes. `--user` recommends for one member from their whole history. The
default evaluates: for each sampled member it hides the last title they rated 4
stars or higher, asks every ranker to order a shortlist holding that title, and
reports mean reciprocal rank and hit rate against popularity, genre match and
the shortlist's own order.

The payload lives in questions.yml. The judgment lives here: the shortlist rule,
the cold-start rule and the blend. evaluate.py holds the evaluation harness: the
hold-out split, the sampled negatives, every metric and the reports.

Usage:
    uv run next_watch.py                          # evaluate 100 members
    uv run next_watch.py --users 100 --seed 3     # a different sample
    uv run next_watch.py --baselines-only         # the free rankers, no key
    uv run next_watch.py --explain 414            # one member, every title
    uv run next_watch.py --user 414               # recommend for one member
    uv run next_watch.py --bootstrap data/movielens-small-100-members-seed-7-jev.json
"""

import argparse
import bisect
import csv
import datetime
import json
import logging
import math
import os
import pathlib
import re
import sys
import time

import yaml
from typesafe_sdk import (
    Choice,
    SystemOneResponse,
    TypeSafeClient,
)

from fetch_movielens import ensure_dataset

# Configure logging with basicConfig
logging.basicConfig(
    level=logging.INFO,  # Set the log level to INFO
    # Define log message format
    format="%(asctime)s,p%(process)s,{%(filename)s:%(lineno)d},%(levelname)s,%(message)s",
)
logger = logging.getLogger(__name__)

# The model pin, the shortlist size, the state budgets and both questions. Sits
# beside this file so `uv run next_watch.py` finds it from any working directory.
PAYLOAD_FILE: pathlib.Path = pathlib.Path(__file__).parent / "questions.yml"

REQUIRED_SETTINGS: tuple[str, ...] = (
    "model",
    "shortlist_size",
    "max_taste_chars",
    "max_likes_chars",
    "max_dislikes_chars",
    "input_usd_per_million",
    "questions",
)

# The SDK reads the key from this variable.
API_KEY_ENV: str = "TYPESAFE_API_KEY"

# Where to look when the environment has no key: beside the sample, then at the
# repo root. The sample reads one variable out of the file and logs the path it
# came from, never the value.
ENV_FILES: tuple[pathlib.Path, ...] = (
    pathlib.Path(__file__).parent / ".env",
    pathlib.Path(__file__).parents[2] / ".env",
)

# A rating at or above this counts as a like: it is what the evaluation hides and
# what the state lists. MovieLens rates in half stars from 0.5 to 5.
LIKED_FLOOR: float = 4.0

# A rating at or below this counts as a dislike. The 3 and 3.5 ratings between the
# two carry little signal for their token cost, so the state leaves them out, the
# way GenRec drops short plays.
DISLIKED_CEILING: float = 2.5

# A member needs this many likes before the held-out one to reach Jev. Below it
# there is too little history to verbalize, and a popularity list serves them at
# no cost.
MIN_LIKED_HISTORY: int = 5

# How many of a member's genres count as "usual" when judging whether a pick
# widens their range.
USUAL_GENRES: int = 3

# How many genres the taste summary names.
TASTE_GENRES: int = 5

# Shortlist slots `--user` saves for the most popular titles outside a member's
# usual genres. Genre match alone fills all twenty with more of the same, which
# leaves the discovery question nothing to pick.
EXPLORE_SLOTS: int = 5

# How many titles `--user` prints.
SHOW_TOP: int = 10

DEFAULT_USERS: int = 100
DEFAULT_SEED: int = 7

NO_GENRES: str = "(no genres listed)"

YEAR_PATTERN: re.Pattern = re.compile(r"\((\d{4})\)\s*$")


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


def _load_movies(folder: pathlib.Path) -> dict[int, dict]:
    """Read movies.csv into one record per title.

    Args:
        folder: The folder fetch_movielens.py extracted into.

    Returns:
        Records keyed by MovieLens id, each holding the title as MovieLens
        writes it, the release year when the title carries one, and the genres.
    """
    movies = {}
    with (folder / "movies.csv").open(encoding="utf-8", newline="") as handle:
        for row in csv.DictReader(handle):
            year = YEAR_PATTERN.search(row["title"])
            genres = [] if row["genres"] == NO_GENRES else row["genres"].split("|")
            movies[int(row["movieId"])] = {
                "id": int(row["movieId"]),
                "title": row["title"].strip(),
                "year": int(year.group(1)) if year else None,
                "genres": genres,
            }
    return movies


def _load_timelines(folder: pathlib.Path) -> dict[int, list[tuple[int, int, float]]]:
    """Read ratings.csv into one timeline per member, oldest first.

    Args:
        folder: The folder fetch_movielens.py extracted into.

    Returns:
        Timelines keyed by member id. Each entry is (timestamp, movie id,
        rating), sorted by time and then by movie id, so a batch of ratings made
        in the same second keeps one fixed order between runs.
    """
    timelines: dict[int, list[tuple[int, int, float]]] = {}
    with (folder / "ratings.csv").open(encoding="utf-8", newline="") as handle:
        for row in csv.DictReader(handle):
            entry = (int(row["timestamp"]), int(row["movieId"]), float(row["rating"]))
            timelines.setdefault(int(row["userId"]), []).append(entry)
    for timeline in timelines.values():
        timeline.sort()
    return timelines


def _popularity_index(timelines: dict[int, list]) -> dict[int, list[int]]:
    """Collect every rating time per title, sorted, so a count before any moment is one bisect.

    Args:
        timelines: Member timelines from _load_timelines.

    Returns:
        Sorted rating timestamps keyed by movie id.
    """
    index: dict[int, list[int]] = {}
    for timeline in timelines.values():
        for timestamp, movie_id, _ in timeline:
            index.setdefault(movie_id, []).append(timestamp)
    for stamps in index.values():
        stamps.sort()
    return index


def _genre_match(
    profile: dict[str, float],
    genres: list[str],
) -> float:
    """Cosine between a member's genre counts and a title's genre tags.

    Args:
        profile: Liked-title counts keyed by genre.
        genres: The title's genres, each weighted 1.

    Returns:
        A number from 0, no shared genre, to 1, the same mix.
    """
    if not profile or not genres:
        return 0.0
    dot = sum(profile.get(genre, 0.0) for genre in genres)
    norm = math.sqrt(sum(value * value for value in profile.values())) * math.sqrt(len(genres))
    return dot / norm


def _shortlist_score(
    profile: dict[str, float],
    match: float,
    popularity: int,
) -> float:
    """Score one title for the free shortlist: genre match times log popularity.

    The log keeps a blockbuster from burying every niche title that fits better.
    A member with no likes has no genre profile, so popularity alone decides.

    Args:
        profile: The member's liked-genre counts.
        match: The title's genre match for this member.
        popularity: Ratings the title had collected before now.

    Returns:
        The shortlist score.
    """
    return (match if profile else 1.0) * math.log1p(popularity)


def _explore_shortlist(
    profile: dict[str, float],
    excluded: set[int],
    before: int,
    movies: dict[int, dict],
    index: dict[int, list[int]],
    size: int,
) -> list[dict]:
    """Fill most of the shortlist by genre match and save a few slots for range.

    Args:
        profile: The member's liked-genre counts.
        excluded: Titles the member has rated.
        before: The moment of recommendation, for popularity.
        movies: Title records keyed by id.
        index: Sorted rating times keyed by movie id.
        size: How many titles to return.

    Returns:
        Candidates: the best genre matches, then the most popular titles outside
        the member's usual genres.
    """
    usual = usual_genres(profile)
    close = build_shortlist(profile, excluded, before, movies, index, size - EXPLORE_SLOTS)
    taken = excluded | {c["id"] for c in close}
    outside = [
        score_candidate(movie_id, profile, before, movies, index)
        for movie_id, movie in movies.items()
        if movie_id not in taken and widens(movie, usual)
    ]
    outside.sort(key=lambda candidate: (-candidate["popularity"], candidate["id"]))
    return close + outside[: size - len(close)]


def _fit_lines(
    lines: list[str],
    budget: int,
) -> str:
    """Join lines until the next one would pass the budget.

    Args:
        lines: Lines in priority order.
        budget: The most characters the field may hold.

    Returns:
        The lines that fit, one per line, or "none" when the list is empty.
    """
    kept: list[str] = []
    used = 0
    for line in lines:
        if used + len(line) + 1 > budget:
            break
        kept.append(line)
        used += len(line) + 1
    return "\n".join(kept) if kept else "none"


def _taste_summary(
    history: list[tuple[int, int, float]],
    profile: dict[str, float],
) -> str:
    """Compress the whole history into two sentences Python can count.

    GenRec keeps recent events in detail and folds older ones into a short
    interest summary. The counting happens here, because Jev cannot count.

    Args:
        history: The member's timeline before the moment of recommendation.
        profile: The member's liked-genre counts.

    Returns:
        How many titles they rated and when, then their genres by share of likes.
    """
    likes = len(liked(history))
    first = datetime.datetime.fromtimestamp(history[0][0], datetime.UTC).year
    last = datetime.datetime.fromtimestamp(history[-1][0], datetime.UTC).year
    ranked = sorted(profile.items(), key=lambda item: (-item[1], item[0]))[:TASTE_GENRES]
    shares = ", ".join(f"{genre} {round(100 * count / likes)}%" for genre, count in ranked)
    return (
        f"Rated {len(history)} titles between {first} and {last}, "
        f"{likes} of them {LIKED_FLOOR:g} stars or higher. "
        f"Share of those likes carrying each genre: {shares}."
    )


def _blend(
    answers: dict,
    specs: dict,
    label: str,
) -> float:
    """Weight each question's probability for one title and average them.

    Args:
        answers: Jev's answers keyed by question id.
        specs: Question entries from questions.yml, each carrying a weight.
        label: The option key, such as `m318`.

    Returns:
        The weighted mean probability.
    """
    total = sum(spec["weight"] for spec in specs.values())
    if total == 0:
        return 0.0
    weighted = sum(
        spec["weight"] * answers[question_id].probabilities.get(label, 0.0)
        for question_id, spec in specs.items()
    )
    return weighted / total


def _recommend_rows(
    shortlist: list[dict],
    scores: dict[str, dict[int, float]],
    answers: dict | None,
    specs: dict,
    usual: set[str],
    movies: dict[int, dict],
) -> tuple[list[str], list[list[str]]]:
    """Build the table `--user` prints, best title first.

    Args:
        shortlist: The candidates.
        scores: Scores keyed by ranker, then movie id.
        answers: Jev's answers, or None.
        specs: Question entries from questions.yml.
        usual: The member's usual genres.
        movies: Title records keyed by id.

    Returns:
        Tuple of (header, rows).
    """
    ranker = "jev blend" if answers else "shortlist order"
    ranked = sorted(shortlist, key=lambda c: -scores[ranker][c["id"]])[:SHOW_TOP]
    header = ["#", "Title", ranker]
    if answers:
        header += [spec["label"] for spec in specs.values()]
    header += ["Popularity", "Genre match", "Widens"]
    rows = []
    for position, c in enumerate(ranked, start=1):
        label = f"m{c['id']}"
        row = [str(position), option_text(movies[c["id"]])[:60], f"{scores[ranker][c['id']]:.3f}"]
        if answers:
            row += [f"{answers[q].probabilities.get(label, 0.0):.3f}" for q in specs]
        range_mark = "yes" if widens(movies[c["id"]], usual) else ""
        row += [str(c["popularity"]), f"{c['genre_match']:.2f}", range_mark]
        rows.append(row)
    return header, rows


def load_payload() -> tuple[dict, dict]:
    """Read the settings and the questions from questions.yml.

    Returns:
        Tuple of (settings, specs). Specs holds each question entry keyed by id,
        in file order.

    Raises:
        SystemExit: If the file is missing a key the run needs, or a question
            is anything other than a weighted Choice.
    """
    payload = yaml.safe_load(PAYLOAD_FILE.read_text(encoding="utf-8"))
    missing = set(REQUIRED_SETTINGS) - payload.keys()
    if missing:
        sys.exit(f"{PAYLOAD_FILE.name}: missing {', '.join(sorted(missing))}")

    specs = payload.pop("questions")
    for question_id, spec in specs.items():
        if spec.get("type") != "choice" or "weight" not in spec:
            sys.exit(f"{PAYLOAD_FILE.name}: {question_id} must be a choice with a weight")
    return payload, specs


def require_api_key() -> None:
    """Put the key in the environment before anything reaches the network.

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
            f"  export {API_KEY_ENV}=your-key\n"
            "  or pass --baselines-only to score the free rankers without one"
        )
    os.environ[API_KEY_ENV] = key


def load_catalog() -> tuple[dict, dict, dict]:
    """Load the titles, the member timelines and the popularity index.

    Returns:
        Tuple of (movies, timelines, popularity index).
    """
    folder = ensure_dataset()
    movies = _load_movies(folder)
    timelines = _load_timelines(folder)
    logger.info(f"Loaded {len(movies):,} titles and {len(timelines):,} members")
    return movies, timelines, _popularity_index(timelines)


def ratings_before(
    index: dict[int, list[int]],
    movie_id: int,
    before: int,
) -> int:
    """Count the ratings a title had collected before a moment.

    Counting only earlier ratings keeps the evaluation honest: a ranker that
    knew how popular a title would become later would be reading the future.

    Args:
        index: Sorted rating times keyed by movie id.
        movie_id: The title to count.
        before: A Unix timestamp; ratings at or after it do not count.

    Returns:
        The number of earlier ratings.
    """
    return bisect.bisect_left(index.get(movie_id, []), before)


def liked(history: list[tuple[int, int, float]]) -> list[tuple[int, int, float]]:
    """Keep the entries rated at or above LIKED_FLOOR.

    Args:
        history: Timeline entries.

    Returns:
        The likes, in timeline order.
    """
    return [entry for entry in history if entry[2] >= LIKED_FLOOR]


def genre_profile(
    history: list[tuple[int, int, float]],
    movies: dict[int, dict],
) -> dict[str, float]:
    """Count how many liked titles carry each genre.

    Args:
        history: The member's timeline before the moment of recommendation.
        movies: Title records keyed by id.

    Returns:
        Liked-title counts keyed by genre.
    """
    profile: dict[str, float] = {}
    for _, movie_id, _ in liked(history):
        for genre in movies[movie_id]["genres"]:
            profile[genre] = profile.get(genre, 0.0) + 1.0
    return profile


def usual_genres(profile: dict[str, float]) -> set[str]:
    """Name the genres a member likes most often.

    Args:
        profile: Liked-title counts keyed by genre.

    Returns:
        The top USUAL_GENRES genres.
    """
    ranked = sorted(profile.items(), key=lambda item: (-item[1], item[0]))
    return {genre for genre, _ in ranked[:USUAL_GENRES]}


def widens(
    movie: dict,
    usual: set[str],
) -> bool:
    """Say whether a title sits wholly outside a member's usual genres.

    Args:
        movie: The title record.
        usual: The member's usual genres.

    Returns:
        True when the member has usual genres, the title has genres, and none of
        the title's is usual. A member with no likes has no range to widen.
    """
    return bool(usual) and bool(movie["genres"]) and not usual.intersection(movie["genres"])


def score_candidate(
    movie_id: int,
    profile: dict[str, float],
    before: int,
    movies: dict[int, dict],
    index: dict[int, list[int]],
) -> dict:
    """Score one title the way build_shortlist scores the rest.

    Args:
        movie_id: The title.
        profile: The member's liked-genre counts.
        before: The moment of recommendation, for popularity.
        movies: Title records keyed by id.
        index: Sorted rating times keyed by movie id.

    Returns:
        A candidate record matching the ones build_shortlist returns.
    """
    popularity = ratings_before(index, movie_id, before)
    match = _genre_match(profile, movies[movie_id]["genres"])
    return {
        "id": movie_id,
        "popularity": popularity,
        "genre_match": match,
        "shortlist_score": _shortlist_score(profile, match, popularity),
    }


def build_shortlist(
    profile: dict[str, float],
    excluded: set[int],
    before: int,
    movies: dict[int, dict],
    index: dict[int, list[int]],
    size: int,
) -> list[dict]:
    """Build the free shortlist: genre match times log popularity, best first.

    This is the retrieval stage in front of the ranker. It needs no model, it
    runs in milliseconds over the whole catalog, and it only proposes titles.

    Args:
        profile: The member's liked-genre counts.
        excluded: Titles the member has rated, which it never proposes.
        before: The moment of recommendation, for popularity.
        movies: Title records keyed by id.
        index: Sorted rating times keyed by movie id.
        size: How many titles to return.

    Returns:
        Candidates holding id, popularity, genre match and shortlist score.
    """
    candidates = []
    for movie_id, movie in movies.items():
        if movie_id in excluded:
            continue
        popularity = ratings_before(index, movie_id, before)
        if popularity == 0:
            continue
        match = _genre_match(profile, movie["genres"])
        candidates.append(
            {
                "id": movie_id,
                "popularity": popularity,
                "genre_match": match,
                "shortlist_score": _shortlist_score(profile, match, popularity),
            }
        )
    candidates.sort(key=lambda candidate: (-candidate["shortlist_score"], candidate["id"]))
    return candidates[:size]


def option_text(movie: dict) -> str:
    """Describe one title as Jev sees it among the options.

    Args:
        movie: The title record.

    Returns:
        The title with its year, then its genres.
    """
    genres = ", ".join(movie["genres"]) or "no genres listed"
    return f"{movie['title']}; {genres}"


def build_state(
    history: list[tuple[int, int, float]],
    profile: dict[str, float],
    movies: dict[int, dict],
    settings: dict,
) -> dict:
    """Verbalize a member's history into three named fields, each on its own budget.

    Args:
        history: The member's timeline before the moment of recommendation.
        profile: The member's liked-genre counts.
        movies: Title records keyed by id.
        settings: Settings from questions.yml.

    Returns:
        The state: a taste summary, recent likes and recent dislikes, newest first.
    """

    def line(entry: tuple[int, int, float]) -> str:
        return f"{option_text(movies[entry[1]])}; rated {entry[2]:g}"

    newest = list(reversed(history))
    likes = [line(entry) for entry in newest if entry[2] >= LIKED_FLOOR]
    dislikes = [line(entry) for entry in newest if entry[2] <= DISLIKED_CEILING]
    return {
        "taste": _taste_summary(history, profile)[: settings["max_taste_chars"]],
        "recent_likes": _fit_lines(likes, settings["max_likes_chars"]),
        "recent_dislikes": _fit_lines(dislikes, settings["max_dislikes_chars"]),
    }


def build_questions(
    specs: dict,
    shortlist: list[dict],
    movies: dict[int, dict],
) -> dict[str, Choice]:
    """Turn each question entry into a Choice over this member's shortlist.

    Args:
        specs: Question entries from questions.yml.
        shortlist: The candidates, in the order Jev should see them.
        movies: Title records keyed by id.

    Returns:
        Choice objects keyed by question id.
    """
    # Treat the option text as untrusted. It is catalog metadata, and a catalog fed
    # by studios or by users can carry a title written to win the pick. The name
    # says where the text came from, so nobody reads it as a fact about the title.
    catalog_text_by_option = {f"m{c['id']}": option_text(movies[c["id"]]) for c in shortlist}
    return {
        question_id: Choice(instructions=spec["instructions"], criteria=catalog_text_by_option)
        for question_id, spec in specs.items()
    }


def ask_jev(
    client: TypeSafeClient,
    state: dict,
    questions: dict[str, Choice],
    settings: dict,
) -> tuple[SystemOneResponse, int]:
    """Send one member's state and both questions in a single call.

    Args:
        client: The SDK client, reused across members.
        state: The verbalized history.
        questions: Choice objects keyed by question id.
        settings: Settings from questions.yml.

    Returns:
        Tuple of (the response, the round trip in milliseconds).
    """
    started = time.perf_counter()
    response = client.system_one(model=settings["model"], state=state, questions=questions)
    return response, round((time.perf_counter() - started) * 1000)


def ranker_scores(
    shortlist: list[dict],
    answers: dict | None,
    specs: dict,
) -> dict[str, dict[int, float]]:
    """Score every candidate under every ranker, higher first.

    The first three need no key: popularity counts ratings before the moment of
    recommendation, genre match is a cosine between the member's liked genres and
    the title's, and shortlist order is the score the rules shortlist by. The
    last two read Jev's answers.

    Args:
        shortlist: The candidates.
        answers: Jev's answers, or None when no call was made.
        specs: Question entries from questions.yml.

    Returns:
        Scores keyed by ranker name, then by movie id.
    """
    scores = {
        "popularity": {c["id"]: float(c["popularity"]) for c in shortlist},
        "genre match": {c["id"]: c["genre_match"] for c in shortlist},
        "shortlist order": {c["id"]: c["shortlist_score"] for c in shortlist},
    }
    if answers is None:
        return scores
    # The first question in questions.yml is the ranking question; the blend adds
    # the rest by weight.
    first = next(iter(specs))
    scores[f"jev {first}"] = {
        c["id"]: answers[first].probabilities.get(f"m{c['id']}", 0.0) for c in shortlist
    }
    scores["jev blend"] = {c["id"]: _blend(answers, specs, f"m{c['id']}") for c in shortlist}
    return scores


def padded_lines(
    header: list[str],
    rows: list[list[str]],
) -> list[str]:
    """Lay out a markdown table with every column padded to its widest cell.

    Args:
        header: Header cells.
        rows: Body rows, each as long as the header.

    Returns:
        The table as lines.
    """
    widths = [max(len(str(cell)) for cell in column) for column in zip(header, *rows)]

    def render(cells: list[str]) -> str:
        return "| " + " | ".join(str(c).ljust(w) for c, w in zip(cells, widths)) + " |"

    divider = "|" + "|".join("-" * (width + 2) for width in widths) + "|"
    return [render(header), divider] + [render(row) for row in rows]


def recommend(
    user_id: int,
    baselines_only: bool = False,
    verbose: bool = False,
) -> None:
    """Recommend for one member from their whole history, and explain every column.

    Args:
        user_id: The member.
        baselines_only: Skip Jev and print the free shortlist's order.
        verbose: Print the raw JSON Jev returned.
    """
    settings, specs = load_payload()
    if not baselines_only:
        require_api_key()
    movies, timelines, index = load_catalog()
    if user_id not in timelines:
        sys.exit(f"member {user_id} is not in MovieLens small, which numbers 1 to {max(timelines)}")

    timeline = timelines[user_id]
    now = timeline[-1][0] + 1
    profile = genre_profile(timeline, movies)
    rated = {movie_id for _, movie_id, _ in timeline}
    if len(liked(timeline)) < MIN_LIKED_HISTORY:
        print(f"Member {user_id} has fewer than {MIN_LIKED_HISTORY} likes: popularity serves them.")
        profile = {}
    size = settings["shortlist_size"]
    shortlist = (
        _explore_shortlist(profile, rated, now, movies, index, size)
        if profile
        else build_shortlist(profile, rated, now, movies, index, size)
    )

    answers = None
    if profile and not baselines_only:
        state = build_state(timeline, profile, movies, settings)
        questions = build_questions(specs, shortlist, movies)
        response, latency_ms = ask_jev(TypeSafeClient(), state, questions, settings)
        answers = response.answers
        if verbose:
            print(json.dumps(response.model_dump(mode="json"), indent=2))
        tokens = response.usage.input_tokens
        cost = tokens * settings["input_usd_per_million"] / 1_000_000
        print(f"One Jev call: {latency_ms} ms, {tokens:,} input tokens, ${cost:.6f}.")

    scores = ranker_scores(shortlist, answers, specs)
    header, rows = _recommend_rows(shortlist, scores, answers, specs, usual_genres(profile), movies)
    print(f"\n## Next watch for member {user_id}\n")
    print("\n".join(padded_lines(header, rows)))
    print(
        f"\nThe rules shortlisted {len(shortlist)} unrated titles: {len(shortlist) - EXPLORE_SLOTS} "
        f"by genre match times log popularity, and the {EXPLORE_SLOTS} most popular outside the "
        "member's usual genres. Each Jev column is the probability that question gave the title, and the "
        "blend weights them by questions.yml. Popularity counts every MovieLens rating before "
        "this member's last one. Genre match runs 0 to 1. Widens marks a title outside the "
        f"member's {USUAL_GENRES} most-liked genres."
    )


def main() -> None:
    """Parse arguments and run an evaluation, one recommendation, or a bootstrap replay."""
    parser = argparse.ArgumentParser(
        description="Rank what a MovieLens member watches next, one Jev call each.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Example usage:
    # Evaluate every ranker on 100 sampled members (downloads MovieLens once)
    uv run next_watch.py

    # A bigger sample, or a different one
    uv run next_watch.py --users 100 --seed 3

    # Score the free rankers alone, with no key and no Jev calls
    uv run next_watch.py --baselines-only

    # Print one member's state and every shortlisted title under every ranker
    uv run next_watch.py --explain 414

    # Recommend for one member from their whole history
    uv run next_watch.py --user 414

    # Recompute the bootstrap from the committed report, with no key and no calls
    uv run next_watch.py --bootstrap data/movielens-small-100-members-seed-7-jev.json

The questions, the weights, the shortlist size and the state budgets live in
questions.yml. The shortlist rule and the cold-start rule live here, and the
metrics live in evaluate.py.
Reads TYPESAFE_API_KEY from the environment, or from .env beside this file or at
the repo root.
""",
    )
    parser.add_argument(
        "--users",
        type=int,
        default=DEFAULT_USERS,
        help=f"How many members to sample for the evaluation (default: {DEFAULT_USERS})",
    )
    parser.add_argument(
        "--seed",
        type=int,
        default=DEFAULT_SEED,
        help=f"Seed for sampling members and shuffling shortlists (default: {DEFAULT_SEED})",
    )
    parser.add_argument(
        "--explain",
        type=int,
        help="Print one member's state and every shortlisted title under every ranker",
    )
    parser.add_argument(
        "--user",
        type=int,
        help="Recommend for one member instead of evaluating",
    )
    parser.add_argument(
        "--bootstrap",
        type=pathlib.Path,
        metavar="REPORT",
        help="Recompute the paired bootstrap from a saved JSON report, with no key and no calls",
    )
    parser.add_argument(
        "--baselines-only",
        action="store_true",
        help="Skip Jev and score the free rankers, which needs no key",
    )
    parser.add_argument(
        "--debug",
        action="store_true",
        help="Turn on debug logging",
    )
    parser.add_argument(
        "--verbose",
        action="store_true",
        help="Pretty print the raw JSON Jev returned",
    )
    args = parser.parse_args()

    if args.debug:
        logging.getLogger().setLevel(logging.DEBUG)

    # evaluate.py imports this module, so main imports it at call time.
    if args.bootstrap is not None:
        from evaluate import replay_bootstrap

        replay_bootstrap(args.bootstrap)
        return
    if args.user is not None:
        recommend(args.user, args.baselines_only, args.verbose)
        return
    from evaluate import evaluate

    evaluate(args.users, args.seed, args.explain, args.baselines_only, args.verbose)


if __name__ == "__main__":
    main()
