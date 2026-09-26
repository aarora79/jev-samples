# Triage: apache/airflow

18 pull requests, of which 8 reached Jev at 11 questions each, one call apiece, on 26 September 2026 with `jev-1.13.0`.
24,317 input tokens, $0.00102 at $0.042 per million.

## Summary

| Outcome             | Count | Pull requests                                                  |
| ------------------- | ----- | -------------------------------------------------------------- |
| draft               | 1     | #73720                                                         |
| ci-failing          | 8     | #73743, #73737, #73734, #73732, #73730, #73726, #73724, #73723 |
| ci-pending          | 1     | #73740                                                         |
| green-is-enough     | 2     | #73742, #73722                                                 |
| tests-are-enough    | 3     | #73719, #73727, #73729                                         |
| ai-review-is-enough | 1     | #73728                                                         |
| human-required      | 2     | #73736, #73741                                                 |

The state rows come first: plain rules settle those before any model call. Each route below them names what would be enough to merge.

## The 8 pull requests that reached Jev

| PR     | Title                                        | Files | Lines    | Kind       | Effort | Cons | Cons from        | Route                       |
| ------ | -------------------------------------------- | ----- | -------- | ---------- | ------ | ---- | ---------------- | --------------------------- |
| #73728 | Discover a bundle's Dag definitions throu... | 3     | +203/-34 | feature    | 0.49   | 0.53 | breaking change  | ai-review-is-enough         |
| #73741 | Improve DAG tag length validation error      | 2     | +11/-3   | bugfix     | 0.40   | 0.97 | security surface | human-required              |
| #73719 | Fix masked failure reason for deferrable ... | 4     | +103/-8  | bugfix     | 0.37   | 0.33 | breaking change  | tests-are-enough (on a cut) |
| #73727 | Speed up zip Dag discovery and keep membe... | 2     | +53/-23  | bugfix     | 0.35   | 0.23 | breaking change  | tests-are-enough            |
| #73736 | Bump astral-sh/setup-uv from 10.1.0 to 10... | 4     | +4/-4    | dependency | 0.28   | 0.99 | infra surface    | human-required              |
| #73742 | Account for the open pull request limit i... | 3     | +36/-0   | docs       | 0.28   | 0.07 | infra surface    | green-is-enough             |
| #73729 | Fix clearing with upstream and downstream... | 2     | +51/-2   | bugfix     | 0.27   | 0.17 | breaking change  | tests-are-enough (on a cut) |
| #73722 | Clarify max_db_retries doc wording to avo... | 1     | +2/-1    | docs       | 0.19   | 0.04 | breaking change  | green-is-enough             |

Effort is the weighted mean of nine questions, 0 to 1, and consequence is the max of four. `Cons from` names the question that produced the consequence, which is what set the route. Effort is raised when size demands it: over 30 files or 1,500 lines is high whatever Jev returned.

## Not reviewable yet (10)


### draft (1)

the author is still working: nothing to review, and nothing to decide

- [#73720](https://github.com/apache/airflow/pull/73720) [v3-3-test] Raise DeadlockImminentError for sync comms calls from a paused event loop thread (#73521)
  - marked draft by the author

### ci-failing (8)

the branch cannot merge until the checks pass, so review waits on that

5 of these fail only on a check that fails elsewhere too, so they are waiting on the checks rather than on their authors.

- [#73743](https://github.com/apache/airflow/pull/73743) Fix airflowctl dags get-tags crashing whenever a Dag has tags
  - 1 of 95 checks failing: Postgres tests: core / DB-core:Postgres:14:3.10:Core...Serialization, which also fails on other pull requests
- [#73737](https://github.com/apache/airflow/pull/73737) Bump the github-actions-updates group with 4 updates
  - 1 of 100 checks failing: Basic tests / Scripts tests, which also fails on other pull requests
- [#73734](https://github.com/apache/airflow/pull/73734) Bump the edge-ui-package-updates group across 1 directory with 8 updates
  - 1 of 75 checks failing: Postgres tests: core / DB-core:Postgres:14:3.10:Core...Serialization, which also fails on other pull requests
- [#73732](https://github.com/apache/airflow/pull/73732) Bump boto3 from 1.43.96 to 1.43.98 in /dev/breeze in the 3-3-uv-dependency-updates group
  - 1 of 100 checks failing: Basic tests / Scripts tests, which also fails on other pull requests
- [#73730](https://github.com/apache/airflow/pull/73730) Bump boto3 from 1.43.97 to 1.43.98 in /dev/breeze in the uv-dependency-updates group
  - 1 of 100 checks failing: Postgres tests: core / DB-core:Postgres:14:3.10:Core...Serialization, which also fails on other pull requests
- [#73726](https://github.com/apache/airflow/pull/73726) UI: Add team filter to Human-in-the-loop task instances listing
  - 2 of 100 checks failing: 2 checks
- [#73724](https://github.com/apache/airflow/pull/73724) Fix Grid and Graph 500 errors for cyclic TaskGroup dependencies
  - 1 of 100 checks failing: Additional PROD image tests / Test e2e integration tests with PROD image / Regular e2e test
- [#73723](https://github.com/apache/airflow/pull/73723) TS SDK: embed one source region per native Dag file
  - 1 of 88 checks failing: Additional PROD image tests / TypeScript SDK e2e tests with PROD image / TypeScript SDK e2e test

### ci-pending (1)

checks are still running: come back when they land

- [#73740](https://github.com/apache/airflow/pull/73740) Bump the auth-ui-package-updates group across 1 directory with 8 updates
  - 1 of 89 checks still running

## human-required (2)

a person reads this before it merges, whatever the machines say

- [#73736](https://github.com/apache/airflow/pull/73736) Bump astral-sh/setup-uv from 10.1.0 to 10.2.0 in the github-actions-updates group
  - consequence 0.99 from infra surface, effort 0.28
  - drivers: infra surface, has tests, blast radius
  - would drop a route with: evidence that infra surface is covered, a test or a reviewer who owns it
- [#73741](https://github.com/apache/airflow/pull/73741) Improve DAG tag length validation error
  - consequence 0.97 from security surface, effort 0.40
  - drivers: security surface, blast radius, mechanical
  - would drop a route with: evidence that security surface is covered, a test or a reviewer who owns it

## ai-review-is-enough (1)

an AI review that finds nothing is sufficient, plus green CI and tests

- [#73728](https://github.com/apache/airflow/pull/73728) Discover a bundle's Dag definitions through the importer registry
  - consequence 0.53 from breaking change, effort 0.49
  - drivers: design decisions, blast radius, mechanical
  - would drop a route with: evidence that breaking change is covered, a test or a reviewer who owns it

## tests-are-enough (3)

merge when CI is green and a test exercises the change

- [#73719](https://github.com/apache/airflow/pull/73719) Fix masked failure reason for deferrable EMR Serverless jobs
  - consequence 0.33 from breaking change, effort 0.37
  - drivers: design decisions, mechanical, blast radius
  - would drop a route with: evidence that breaking change is covered, a test or a reviewer who owns it
- [#73727](https://github.com/apache/airflow/pull/73727) Speed up zip Dag discovery and keep member file names
  - consequence 0.23 from breaking change, effort 0.35
  - drivers: design decisions, blast radius, mechanical
  - would drop a route with: evidence that breaking change is covered, a test or a reviewer who owns it
- [#73729](https://github.com/apache/airflow/pull/73729) Fix clearing with upstream and downstream selecting unrelated tasks
  - consequence 0.17 from breaking change, effort 0.27
  - drivers: blast radius, design decisions, mechanical
  - would drop a route with: evidence that breaking change is covered, a test or a reviewer who owns it

## green-is-enough (2)

merge when CI is green: nothing here needs a person

- [#73742](https://github.com/apache/airflow/pull/73742) Account for the open pull request limit in the PR triage process
  - consequence 0.07 from infra surface, effort 0.28
  - drivers: mechanical, has tests, design decisions
- [#73722](https://github.com/apache/airflow/pull/73722) Clarify max_db_retries doc wording to avoid off-by-one confusion
  - consequence 0.04 from breaking change, effort 0.19
  - drivers: has tests
