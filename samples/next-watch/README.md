# next-watch

Ranks what a MovieLens member watches next with one Jev call per member, and measures the ranking against popularity, genre match and a random draw on the same candidates.

The shape comes from GenRec, the LLM-backed ranker Netflix described in its [tech blog on 30 July 2026](https://netflixtechblog.medium.com/genrec-towards-llm-native-recommendation-at-netflix-f20be6f643e3) and in [arXiv 2608.10257](https://arxiv.org/abs/2608.10257). GenRec writes a member's history out as text, reads it once in a prefill-only pass, and scores every candidate title in that same pass. A Jev `Choice` does the same thing: it reads the state once and returns a probability for every option. This sample puts the member's history in the state and the shortlist in the options, and ranks by the probabilities that come back.

| GenRec | next-watch |
| --- | --- |
| Verbalized history and context | A `state` with three named fields, each on its own character budget |
| Keep high-signal events, drop low-signal ones, fold old history into a summary | Likes (4 stars or more) and dislikes (2.5 or less) newest first, 3 and 3.5 ratings dropped, the rest counted into a taste summary |
| Candidate set from upstream retrieval | A free shortlist: genre match times log popularity |
| Catalog-aware scoring head over the candidates | `next_watch`, one `Choice` over twenty titles |
| Reward models for exploration and long-term value | `discovery`, a second `Choice` over the same titles, blended in by a weight in `questions.yml` |
| Offline MRR against the production ranker | MRR and Hit@k against three free rankers on held-out likes |

GenRec trains on Netflix's engagement logs and learns an embedding for every title. Jev trains on nothing here: it reads each title's name, year and genres cold. The evaluation measures what that costs.

## How it works

1. `fetch_movielens.py` downloads MovieLens `ml-latest-small` (100,836 ratings from 610 members over 9,742 titles) from GroupLens once, and refuses the zip if its MD5 differs from the one GroupLens publishes beside it.
2. For each sampled member, the sample hides the last title they rated 4 stars or higher and keeps only the ratings made before it. Ratings made in the same second as the hidden one stay out, because members rate in batches.
3. Rules settle the members with too little to go on. Fewer than five likes before the hidden one, and a popularity list serves them with no call.
4. Everyone else gets twenty candidates: the hidden title, plus nineteen titles they never rated, drawn in proportion to how often other members had rated each one by then. Popularity counts only ratings made before the moment of recommendation, so no ranker reads the future.
5. One call asks both questions over the shuffled twenty. Python blends the two probability spreads by weight and ranks.
6. Every ranker orders the same twenty, and the sample reports where the hidden title landed.

`next_watch.py` holds the shortlist, the state, the Jev call and the blend, and runs every command below; `evaluate.py` holds the hold-out split, the metrics and the reports.

`--user` runs the recommender itself: it takes a member's whole history, shortlists twenty unrated titles (fifteen by genre match times log popularity, five of the most popular outside the member's usual genres), and prints the top ten with every column explained.

Everything the sample sends sits in [`questions.yml`](questions.yml): the model pin, the shortlist size, the three state budgets, and both questions with their weights.

| Question id | Type | Weight | Asks |
| --- | --- | --- | --- |
| `next_watch` | `Choice` | 1.0 | Which of these titles would this member most enjoy watching next? |
| `discovery` | `Choice` | 0.25 | Which of these titles would widen the range of what this member watches? |

The options change per member, so `next_watch.py` fills in `criteria` at run time: each key is a MovieLens id such as `m318`, and each description reads `Shawshank Redemption, The (1994); Crime, Drama`.

## Prerequisites

- Python 3.11 or newer
- [uv](https://docs.astral.sh/uv/)
- A TypeSafe API key, except for `--baselines-only`

## Run it

```bash
cd samples/next-watch

# The key comes from TYPESAFE_API_KEY, or from .env beside the sample or at the
# repo root. Export it to override the file.
export TYPESAFE_API_KEY="your-key"   # optional once ../../.env exists

# Download MovieLens once (the other commands do this on first use too)
uv run fetch_movielens.py

# Evaluate every ranker on 100 sampled members
uv run next_watch.py

# The free rankers alone, with no key and no calls
uv run next_watch.py --baselines-only

# One member's state, and every shortlisted title under every ranker
uv run next_watch.py --explain 414

# Recommend for one member from their whole history
uv run next_watch.py --user 414
```

Each evaluation writes a JSON report holding every answer as Jev returned it, and a markdown copy of the printed tables, into `data/`.

Output from `uv run next_watch.py` on 30 September 2026 against `jev-1.13.0`, seed 7, 100 members. The report behind it sits in [`data/`](data/):

```text
| Ranker            | MRR   | Hit@1 | Hit@5 | Widens | Distinct #1 |
|-------------------|-------|-------|-------|--------|-------------|
| random (expected) | 0.180 | 0.050 | 0.250 | n/a    | n/a         |
| popularity        | 0.178 | 0.050 | 0.265 | 0.180  | 54          |
| genre match       | 0.294 | 0.132 | 0.412 | 0.000  | 89          |
| shortlist order   | 0.253 | 0.100 | 0.430 | 0.000  | 74          |
| jev next_watch    | 0.431 | 0.255 | 0.648 | 0.020  | 86          |
| jev blend         | 0.411 | 0.255 | 0.565 | 0.020  | 86          |

100 Jev calls, 2 questions each. 242,771 input tokens, 102 ms per call on average,
$0.01020 at $0.042 per million input tokens, $0.000102 per member.
```

Jev put the hidden title first for 25.5 of 100 members, against 13.2 for genre match and 5 for a random draw. A paired bootstrap over the 100 members (10,000 resamples of the committed report) puts `jev next_watch` ahead of genre match by 0.137 MRR, with a 95% interval of 0.070 to 0.206. Jev ranked the hidden title higher for 64 members and lower for 26. The 102 ms is the round trip from the machine that ran the evaluation, measured by the sample around each call. TypeSafe's own latency figures remain unreproduced outside the company.

Output from `uv run next_watch.py --user 414` in the same session, first five rows. One call took 130 ms and 2,818 input tokens:

```text
| #  | Title                                                        | jev blend | next watch | discovery | Popularity | Genre match | Widens |
|----|--------------------------------------------------------------|-----------|------------|-----------|------------|-------------|--------|
| 1  | Intouchables (2011); Comedy, Drama                           | 0.256     | 0.320      | 0.000     | 35         | 0.80        |        |
| 2  | 12 Angry Men (1957); Drama                                   | 0.184     | 0.230      | 0.000     | 56         | 0.69        |        |
| 3  | Howl's Moving Castle (Hauru no ugoku shiro) (2004); Adventur | 0.158     | 0.060      | 0.550     | 39         | 0.31        | yes    |
| 4  | Harry Potter and the Prisoner of Azkaban (2004); Adventure,  | 0.146     | 0.180      | 0.010     | 89         | 0.20        | yes    |
| 5  | Life Is Beautiful (La Vita è bella) (1997); Comedy, Drama, R | 0.074     | 0.090      | 0.010     | 85         | 0.72        |        |
```

`discovery` put 0.55 on Howl's Moving Castle, one of the five titles the shortlist saved from outside member 414's usual genres, and the blend lifted it from fifth on `next_watch` alone to third.

## What to notice

**Retrieval and ranking get separate numbers.** On that run the free shortlist would have found the hidden title for 13 of 100 members on its own. A ranker can only reorder what retrieval hands it, so the sample adds the hidden title to every candidate set and scores the rankers on ordering alone. GenRec reports its ranker the same way: its paper ranks a candidate set when one is given.

**Popularity-matched negatives neutralize popularity.** The nineteen other titles come from a draw weighted by popularity, the protocol SASRec and BERT4Rec report against. Popularity then lands at random (0.178 against 0.180), which is the point: a ranker has to know something about this member to beat it. Negatives drawn from the shortlist's own top twenty would stack the list against the hidden title on exactly the features the free rankers sort by, and every free ranker would score below random. An earlier draft of this sample did that, and measured it.

**Genre match is the number to beat.** It reads the same genres Jev sees and scores 0.294 MRR. Jev also reads titles, years and which titles the member disliked, and it scored 0.431. That gap is the value of reading the history as text instead of counting its tags, and nothing trained on this data to get it.

**At a weight of 0.25, the blend cost accuracy and bought no range.** `Widens` counts members whose top pick sits outside their three most-liked genres, and `Distinct #1` counts how many different top picks the run made. The blend left both where `next_watch` put them (0.020 and 86), changed no member's top pick, and lowered MRR by 0.020 (95% interval 0.011 to 0.029). It reorders the middle of the list, as member 414's table shows, without touching the top. GenRec's exploration rewards buy range at training time; at inference time a weight has to beat a confident `next_watch` answer, and 0.25 rarely does. Tuning that weight on these 100 members would fit it to the test set, so sweep it on a different `--seed` and read the trade there.

**Ties count as the average over every order they allow.** Genre match gives every title with the same genres the same score, and Jev often returns 0.0 for more than one option. Breaking ties by list position would credit a ranker for where the shuffle put the answer.

**The state stays near 1,000 tokens.** Three fields, three budgets, filled line by line newest first, and no title arrives cut in half. GenRec cut its context from about 5,000 tokens to about 1,700 with negligible loss in offline ranking quality ([paper, section 5.4](https://arxiv.org/abs/2608.10257)). Shrink `max_likes_chars` and re-run to find this sample's elbow.

**Titles and genres are the only item features.** MovieLens carries no synopsis, so Jev ranks from what a title's name and genres suggest to a model that has read about films. A catalog with descriptions would put them in each option's text, and the Python would not change.

**The options are catalog text, so they are untrusted.** Jev reads each title's name, year and genres as the thing it chooses among, and the titles in `recent_likes` and `recent_dislikes` come from the same catalog. MovieLens is fixed, so nothing here can argue for itself. A catalog fed by studios or by users can, and code that copies this shape should treat its option text the way `pr-triage` treats a pull request description.

## Data and license

The sample uses MovieLens `ml-latest-small` from [GroupLens Research](https://grouplens.org/datasets/movielens/) at the University of Minnesota, which neither endorses nor reviewed this sample. The license allows research use, forbids commercial use without permission, and asks that anyone redistributing the data or transformations of it do so under the same terms. The reports in `data/` name MovieLens titles and ids, and travel under those terms. The downloaded CSVs stay out of git.

F. Maxwell Harper and Joseph A. Konstan. 2015. The MovieLens Datasets: History and Context. ACM Transactions on Interactive Intelligent Systems (TiiS) 5, 4, Article 19 (December 2015), 19 pages. <https://doi.org/10.1145/2827872>
