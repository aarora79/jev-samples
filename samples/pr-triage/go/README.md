# pr-triage, as one binary

The Python sample one directory up is canonical. This is the same check compiled into a single static executable, for a machine with no Python: a CI runner, a bastion host, a developer laptop that should not grow a virtualenv to sort a review queue.

One binary does both halves. It fetches pull requests from the GitHub API and asks Jev eleven questions about each one, so nothing has to run before it.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/aarora79/jev-samples/main/samples/pr-triage/go/install.sh | sh
```

The script resolves the newest `pr-triage/` release, picks the build for the platform, checks it against the published `SHA256SUMS`, and drops it in `/usr/local/bin` or `~/.local/bin`. `VERSION` and `BINDIR` override both choices.

To install the skill alongside the binary, so an agent knows when to reach for it, use [`../vend/install.sh`](../vend/install.sh) instead.

## Run it

```bash
pr-triage apache/airflow                      # the 10 most recent open ones
pr-triage apache/airflow -all                 # every open one
pr-triage apache/airflow -since 30            # opened in the last 30 days
pr-triage apache/airflow -since 2026-08-01    # opened on or after a date
pr-triage apache/airflow -explain 73713       # one pull request in full
pr-triage apache/airflow -fetch-only          # build the dataset, skip Jev
pr-triage -dataset data/apache-airflow-open-all.json
```

Flags can sit on either side of the repository, because the binary reorders them before parsing. Go's `flag` package stops at the first non-flag argument on its own, which would leave `-all` unread.

## Credentials

Each one reads the flag first and the environment second.

| What | Flag | Environment |
| --- | --- | --- |
| GitHub | `-token` | `GITHUB_TOKEN`, `GH_TOKEN`, `GH_ENTERPRISE_TOKEN`, then `gh auth token` |
| TypeSafe | `-jev-key` | `TYPESAFE_API_KEY` |

`-fetch-only` needs the GitHub token and no TypeSafe key. `-dataset` needs the TypeSafe key and no GitHub token. Splitting a run that way lets the half that touches your source code and the half that calls an external API hold different credentials, and run in different places.

Without a GitHub token the API allows sixty requests an hour, and one pull request costs two of them.

## GitHub Enterprise Server

The API base is a variable, not a glued-together path, so an enterprise host needs no patch. Three ways to set it, highest precedence first:

```bash
pr-triage owner/repo -api-base https://ghe.example.com/api/v3   # the flag
pr-triage https://ghe.example.com/owner/repo                    # from the URL host
export GITHUB_API_URL=https://ghe.example.com/api/v3            # the environment
```

A URL on `github.com` resolves to `https://api.github.com`. A URL on any other host resolves to `https://that-host/api/v3`, which is where GHES serves the v3 API. `GITHUB_API_URL` is the variable GitHub Actions already sets on both, so a workflow needs no flag.

The progress line says which API it chose, so a wrong guess shows up before any call:

```
repository acme/widgets through https://ghe.example.com/api/v3
```

## What it writes

Two files per run, into `-out` (default `./data`), plus the dataset when it fetched one:

- `<repo>-<selector>-triage.json`, every answer as Jev sent it, beside the credit, the tier and the size floor
- `<repo>-<selector>-triage.md`, the table and the tier sections, ready to paste into a pull request

Both match the Python sample's reports field for field, and the dataset matches too, so a dataset or report written by either tool reads in the other.

## Gating CI

`-fail-on-tier` exits 2 when any pull request lands in that tier or above:

```bash
pr-triage owner/repo -all -fail-on-tier high -quiet
```

| Exit | Meaning |
| --- | --- |
| 0 | finished, nothing tripped a gate |
| 1 | could not finish: a bad flag, no key, an API that refused |
| 2 | a gate fired |

`-quiet` drops the progress lines from standard error and leaves the report on standard output, which is what a job that captures output wants.

## Build it yourself

```bash
./build.sh 0.1.0
```

That copies the canonical `../questions.yml` in, runs the tests, cross-compiles for linux, darwin and windows on amd64 and arm64, and writes `SHA256SUMS` beside the binaries in `dist/`. Every build is static (`CGO_ENABLED=0`) and stripped (`-s -w`), which takes a binary from 10.4 MB to about 7 MB.

A plain build works too:

```bash
go build -o pr-triage . && ./pr-triage -version
```

## The payload, and why a test guards it

`questions.yml` in this folder is a copy. The Python sample owns the canonical one, and `go:embed` cannot read outside its own directory, so `build.sh` copies it in before every build and `payload_test.go` fails when the two drift:

```
../questions.yml and the embedded copy differ: run ./build.sh, which copies the canonical file in
```

Without that test the two tools would score the same pull request differently, for a reason nobody could see from either side.

`-questions path.yml` points at a payload on disk instead, which is how you try a different weight without rebuilding.

## Where this differs from the Python

Nothing in the judgment: the same eleven questions, the same weights, the same tier cuts, the same 0.02 deadband, the same size floor. Two differences worth knowing:

**The legend is read loosely.** Jev echoes each rubric level back in a `legend`, and one level in the current payload arrives as an object rather than a string, because `- None: some text` is YAML for a mapping. The Go side accepts any value there, so a released binary keeps working against an older payload.

**Repeat runs move a load by about 0.02**, the same as the Python. A tier within the deadband of a cut prints `(on a cut)` rather than picking a side, so two runs of one queue agree with each other.
