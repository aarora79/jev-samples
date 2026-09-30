## next-watch: 100 of 100 sampled members ranked, seed 7

| Ranker            | MRR   | Hit@1 | Hit@5 | Widens | Distinct #1 |
|-------------------|-------|-------|-------|--------|-------------|
| random (expected) | 0.180 | 0.050 | 0.250 | n/a    | n/a         |
| popularity        | 0.178 | 0.050 | 0.265 | 0.180  | 54          |
| genre match       | 0.294 | 0.132 | 0.412 | 0.000  | 89          |
| shortlist order   | 0.253 | 0.100 | 0.430 | 0.000  | 74          |
| jev next_watch    | 0.431 | 0.255 | 0.648 | 0.020  | 86          |
| jev blend         | 0.411 | 0.255 | 0.565 | 0.020  | 86          |

Each ranker ordered the same 20 titles per member: the title the member liked last, which was hidden, and 19 titles they never rated, drawn in proportion to how often everyone else had rated them by then.
MRR is the mean of 1 / (the rank that title landed at), so 1.0 means first every time and 0.050 means last every time. Hit@k is the share of members whose title landed in the top k. Ties count as the average over every order the tie allows.
Widens is the share of members whose #1 pick carries none of their 3 most-liked genres. Distinct #1 counts different #1 picks across all 100 members, so a ranker that shows everyone the same blockbuster scores 1.
Retrieval is its own number: the free shortlist of 20 would have found the hidden title for 13 of 100 members. A ranker can only reorder what retrieval hands it.
Rules settled 0 members at no cost: fewer than 5 likes before the hidden one, so popularity serves them. 0 members never rated anything 4 stars or higher, so they hold nothing to hide.

100 Jev calls, 2 questions each. 242,771 input tokens, 102 ms per call on average, $0.01020 at $0.042 per million input tokens, $0.000102 per member.
