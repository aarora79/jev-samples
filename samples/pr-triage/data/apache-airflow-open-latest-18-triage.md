# Triage: apache/airflow

18 pull requests, 11 questions each, one call apiece, on 25 September 2026 with `jev-1.13.0`.
87,483 input tokens, $0.00367 at $0.042 per million.

| PR     | Files | Lines      | Kind    | Load | Tier              | Review focus | Title                                                      |
| ------ | ----- | ---------- | ------- | ---- | ----------------- | ------------ | ---------------------------------------------------------- |
| #73698 | 8     | +626/-73   | bugfix  | 0.63 | high (on a cut)   | security     | Bind the AWS auth manager SAML response to the browser ... |
| #73696 | 7     | +136/-5    | bugfix  | 0.53 | medium            | security     | Check admin-only views against a dedicated Keycloak res... |
| #73706 | 17    | +2634/-117 | bugfix  | 0.51 | high (size)       | correctness  | Account for AgentOperator spend on failed runs and acro... |
| #73704 | 9     | +57/-32    | bugfix  | 0.50 | medium            | correctness  | Fix socket leaks and add missing request timeouts acros... |
| #73701 | 4     | +360/-39   | feature | 0.46 | medium            | correctness  | Add durable reconnect option to EmrServerlessStartJobOp... |
| #73723 | 10    | +558/-121  | feature | 0.44 | medium (on a cut) | correctness  | TS SDK: embed one source region per native Dag file        |
| #73703 | 5     | +64/-0     | feature | 0.43 | medium (on a cut) | correctness  | [chart/v1-2x-test] Helm: Allow hostAliases and log groo... |
| #73702 | 4     | +291/-3    | feature | 0.42 | low (on a cut)    | correctness  | Stop AWS Glue job run when a deferred task is cleared      |
| #73713 | 54    | +467/-131  | feature | 0.37 | high (size)       | correctness  | UI: Group digits of counters according to the selected ... |
| #73724 | 4     | +209/-12   | bugfix  | 0.37 | low               | correctness  | Fix Grid and Graph 500 errors for cyclic TaskGroup depe... |
| #73720 | 2     | +98/-6     | bugfix  | 0.37 | low               | correctness  | [v3-3-test] Raise DeadlockImminentError for sync comms ... |
| #73719 | 4     | +103/-8    | bugfix  | 0.36 | low               | correctness  | Fix masked failure reason for deferrable EMR Serverless... |
| #73709 | 2     | +113/-30   | bugfix  | 0.30 | low               | correctness  | Avoid repeated KubernetesExecutor pod deletion for dupl... |
| #73718 | 2     | +14/-2     | bugfix  | 0.30 | low               | correctness  | Fix merge_dicts crash when overwriting a non-dict value... |
| #73717 | 3     | +16/-6     | feature | 0.29 | low               | correctness  | add bundle_name to dag processor timeouts metric           |
| #73711 | 2     | +89/-0     | bugfix  | 0.26 | low               | correctness  | Emit queued_duration metric when a task enters RUNNING ... |
| #73722 | 1     | +2/-1      | docs    | 0.19 | trivial           | correctness  | Clarify max_db_retries doc wording to avoid off-by-one ... |
| #73725 | 4     | +32/-8     | bugfix  | 0.18 | trivial           | correctness  | Fix Elasticsearch and OpenSearch response wrapper bugs     |

Load is the weighted average of nine questions, 0 to 1. Tier comes from that load, raised when size demands it: over 30 files or 1,500 lines is high whatever Jev returned.

## high (3)

a human reads this line by line, and the author walks them through it

- [#73698](https://github.com/apache/airflow/pull/73698) load 0.63: Bind the AWS auth manager SAML response to the browser that started the login
  - drivers: security surface, design decisions, mechanical
- [#73706](https://github.com/apache/airflow/pull/73706) load 0.51, raised by the size floor: Account for AgentOperator spend on failed runs and across retries
  - drivers: design decisions, blast radius, breaking change
  - read from 47% of the changed files, so the load is a read on part of the diff
- [#73713](https://github.com/apache/airflow/pull/73713) load 0.37, raised by the size floor: UI: Group digits of counters according to the selected locale
  - drivers: blast radius, design decisions, scope creep
  - read from 44% of the changed files, so the load is a read on part of the diff

## medium (5)

one reviewer who knows this area, reading the whole diff

- [#73696](https://github.com/apache/airflow/pull/73696) load 0.53: Check admin-only views against a dedicated Keycloak resource in multi-team mode
  - drivers: security surface, design decisions, mechanical
- [#73704](https://github.com/apache/airflow/pull/73704) load 0.50: Fix socket leaks and add missing request timeouts across providers
  - drivers: security surface, blast radius, has tests
- [#73701](https://github.com/apache/airflow/pull/73701) load 0.46: Add durable reconnect option to EmrServerlessStartJobOperator
  - drivers: design decisions, mechanical, blast radius
- [#73723](https://github.com/apache/airflow/pull/73723) load 0.44: TS SDK: embed one source region per native Dag file
  - drivers: design decisions, blast radius, mechanical
- [#73703](https://github.com/apache/airflow/pull/73703) load 0.43: [chart/v1-2x-test] Helm: Allow hostAliases and log groomer lifecycle hooks on the Dag processor (#73159)
  - drivers: design decisions, mechanical, blast radius

## low (8)

one reviewer, one pass, no meeting

- [#73702](https://github.com/apache/airflow/pull/73702) load 0.42: Stop AWS Glue job run when a deferred task is cleared
  - drivers: design decisions, mechanical, blast radius
- [#73724](https://github.com/apache/airflow/pull/73724) load 0.37: Fix Grid and Graph 500 errors for cyclic TaskGroup dependencies
  - drivers: design decisions, blast radius, mechanical
- [#73720](https://github.com/apache/airflow/pull/73720) load 0.37: [v3-3-test] Raise DeadlockImminentError for sync comms calls from a paused event loop thread (#73521)
  - drivers: design decisions, blast radius, mechanical
- [#73719](https://github.com/apache/airflow/pull/73719) load 0.36: Fix masked failure reason for deferrable EMR Serverless jobs
  - drivers: design decisions, mechanical, blast radius
- [#73709](https://github.com/apache/airflow/pull/73709) load 0.30: Avoid repeated KubernetesExecutor pod deletion for duplicate events
  - drivers: design decisions, blast radius, mechanical
- [#73718](https://github.com/apache/airflow/pull/73718) load 0.30: Fix merge_dicts crash when overwriting a non-dict value with a dict
  - drivers: blast radius, mechanical, design decisions
- [#73717](https://github.com/apache/airflow/pull/73717) load 0.29: add bundle_name to dag processor timeouts metric
  - drivers: blast radius, mechanical, design decisions
- [#73711](https://github.com/apache/airflow/pull/73711) load 0.26: Emit queued_duration metric when a task enters RUNNING via the execution API
  - drivers: design decisions, blast radius, mechanical

## trivial (2)

merge on a glance: read the title, skim the diff, check that CI is green

- [#73722](https://github.com/apache/airflow/pull/73722) load 0.19: Clarify max_db_retries doc wording to avoid off-by-one confusion
  - drivers: has tests
- [#73725](https://github.com/apache/airflow/pull/73725) load 0.18: Fix Elasticsearch and OpenSearch response wrapper bugs
  - drivers: blast radius
