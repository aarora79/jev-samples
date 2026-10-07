# What to work on in agentic-community/mcp-gateway-registry

25 closed issues read on 7 October 2026, scored with two jev-1.13.0 calls each: one that reads the issue text, one that reads a summary of the answers and decides when to pick it up.
The run took 27s and cost $0.0022 for 53,076 input tokens.

The four buckets are advice. Nothing here was closed, labelled or commented on, and the ordering inside each bucket is the score the model returned.

| horizon           | issues | numbers                                                       |
| ----------------- | ------ | ------------------------------------------------------------- |
| Today             | 2      | #1834, #1724                                                  |
| This week         | 3      | #1348, #1625, #387                                            |
| This month        | 10     | #598, #304, #1665, #1477, #656, #917, #1119, #297, #249, #227 |
| When time permits | 10     | #492, #204, #37, #200, #82, #80, #38, #203, #1229, #1459      |

## Today

The 2 issue(s) the model would start on now.

| issue                                                                          | title                                                                    | kind       | area    | why it is here                                                               |
| ------------------------------------------------------------------------------ | ------------------------------------------------------------------------ | ---------- | ------- | ---------------------------------------------------------------------------- |
| [#1834](https://github.com/agentic-community/mcp-gateway-registry/issues/1834) | 1.31.0 Regression: browser login (OAuth2 callback) fails behind an HTTP( | regression | backend | says work is blocked (0.55), security (0.95), urgent in the body (2.00 of 2) |
| [#1724](https://github.com/agentic-community/mcp-gateway-registry/issues/1724) | fix(a2a): build the agent-card route from the URL origin, not from the J | defect     | backend | on a bucket edge at 2.53, urgent in the body (1.59 of 2), has a repro        |

## This week

| issue                                                                          | title                                                                    | kind   | area                   | why it is here                                                                  |
| ------------------------------------------------------------------------------ | ------------------------------------------------------------------------ | ------ | ---------------------- | ------------------------------------------------------------------------------- |
| [#1348](https://github.com/agentic-community/mcp-gateway-registry/issues/1348) | DocumentDB secret rotation fails: Lambda IAM role grants `docdb:*` but b | defect | infrastructure-as-code | on a bucket edge at 2.45, security (0.94), urgent in the body (1.83 of 2)       |
| [#1625](https://github.com/agentic-community/mcp-gateway-registry/issues/1625) | MCP proxy forwards ingress `X-Forwarded-Proto` to upstream servers, caus | defect | backend                | says work is blocked (0.52), urgent in the body (1.68 of 2), has a repro        |
| [#387](https://github.com/agentic-community/mcp-gateway-registry/issues/387)   | Failed to download transformer model causes issues registering server    | defect | backend                | says work is blocked (0.57), performance (0.66), urgent in the body (1.53 of 2) |

## This month

| issue                                                                          | title                                                                    | kind            | area                   | why it is here                                                     |
| ------------------------------------------------------------------------------ | ------------------------------------------------------------------------ | --------------- | ---------------------- | ------------------------------------------------------------------ |
| [#598](https://github.com/agentic-community/mcp-gateway-registry/issues/598)   | Python Security - Fix B113 (Missing Request Timeouts)                    | enhancement     | backend                | on a bucket edge at 1.45, security (0.84), has a repro             |
| [#304](https://github.com/agentic-community/mcp-gateway-registry/issues/304)   | Nginx service inside registry container has default config enabled       | defect          | deployment-and-ops     | on a bucket edge at 1.38, untouched for 282 days, outside reporter |
| [#1665](https://github.com/agentic-community/mcp-gateway-registry/issues/1665) | Security: add application-layer encryption for per-user egress credentia | feature-request | backend                | security (0.95), proposes a fix, opened 7 days ago                 |
| [#1477](https://github.com/agentic-community/mcp-gateway-registry/issues/1477) | Allow more than 8 hours TTL for MCP access JWT                           | question        | backend                | 3 people in the thread, security (0.88), outside reporter          |
| [#656](https://github.com/agentic-community/mcp-gateway-registry/issues/656)   | Add Okta Identity Provider Support to Helm Charts                        | enhancement     | infrastructure-as-code | security (0.85), has a repro, proposes a fix                       |
| [#917](https://github.com/agentic-community/mcp-gateway-registry/issues/917)   | Feature Request: Dynamic Access Token Injection for Webhook Invocation   | feature-request | backend                | security (0.97), proposes a fix, untouched for 156 days            |
| [#1119](https://github.com/agentic-community/mcp-gateway-registry/issues/1119) | Keycloak: cleanup of accumulated DCR'd clients                           | debt            | backend                | security (0.79), proposes a fix, untouched for 135 days            |
| [#297](https://github.com/agentic-community/mcp-gateway-registry/issues/297)   | Implement Unified UI Registration Flow for MCP Servers and A2A Agents    | feature-request | frontend               | on a bucket edge at 0.58, proposes a fix, untouched for 267 days   |
| [#249](https://github.com/agentic-community/mcp-gateway-registry/issues/249)   | Implement service management API endpoints                               | feature-request | backend                | on a bucket edge at 0.54, security (0.80), proposes a fix          |
| [#227](https://github.com/agentic-community/mcp-gateway-registry/issues/227)   | Implement health checks for A2A agents (background async task)           | feature-request | backend                | on a bucket edge at 0.50, proposes a fix, untouched for 321 days   |

## When time permits

Everything else, in score order. Worth a skim for anything miscategorised rather than a plan.

| issue                                                                          | title                                                                    | kind            | area               | why it is here                                                                    |
| ------------------------------------------------------------------------------ | ------------------------------------------------------------------------ | --------------- | ------------------ | --------------------------------------------------------------------------------- |
| [#492](https://github.com/agentic-community/mcp-gateway-registry/issues/492)   | Add Configuration Viewer Panel with Export to Settings Page              | feature-request | frontend           | on a bucket edge at 0.48, proposes a fix, untouched for 230 days                  |
| [#204](https://github.com/agentic-community/mcp-gateway-registry/issues/204)   | Implement ASOR Federation for Enterprise Agent Discovery                 | feature-request | backend            | on a bucket edge at 0.42, security (0.76), proposes a fix                         |
| [#37](https://github.com/agentic-community/mcp-gateway-registry/issues/37)     | Feature Request: Multi-Level Registry Support                            | feature-request | backend            | on a bucket edge at 0.37, security (0.96), proposes a fix                         |
| [#200](https://github.com/agentic-community/mcp-gateway-registry/issues/200)   | Create Integration Tests & Demo Workflow                                 | enhancement     | backend            | proposes a fix, untouched for 267 days                                            |
| [#82](https://github.com/agentic-community/mcp-gateway-registry/issues/82)     | Integrate Tools Mem0 Memory System                                       | feature-request | backend            | proposes a fix, untouched for 411 days                                            |
| [#80](https://github.com/agentic-community/mcp-gateway-registry/issues/80)     | Design and Implement Base Meta-Agent Class                               | feature-request | backend            | proposes a fix, untouched for 411 days                                            |
| [#38](https://github.com/agentic-community/mcp-gateway-registry/issues/38)     | Feature Request: Usage Metrics and Analytics System                      | feature-request | backend            | proposes a fix, untouched for 369 days                                            |
| [#203](https://github.com/agentic-community/mcp-gateway-registry/issues/203)   | Deploy MCP Gateway Registry on AWS ECS Fargate                           | feature-request | deployment-and-ops | untouched for 316 days                                                            |
| [#1229](https://github.com/agentic-community/mcp-gateway-registry/issues/1229) | Surface canonical server.json via registry MCP tools and make it the def | enhancement     | frontend           | proposes a fix, untouched for 113 days, opened 2 days ago                         |
| [#1459](https://github.com/agentic-community/mcp-gateway-registry/issues/1459) | Posted in error - closing                                                | question        | unclear            | outside reporter commented 1 time(s), no reply, proposes a fix, opened 0 days ago |

---

Buckets come from one question per issue, asked over a summary of eight questions about the text and the facts GitHub already knows. A row in the wrong bucket is usually a question worth rewording: the reasons column says which signal put it there. The questions live in `questions.yml`.
