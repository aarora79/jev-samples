# agents-md-readiness as a single binary

The Python sample one directory up is the canonical implementation. This folder compiles the same judgement into one static executable, for a CI job or a laptop that has no Python, no uv and no SDK.

A user downloads one file and runs it. Nothing else installs: the Go runtime links into the binary, `CGO_ENABLED=0` drops the libc dependency, and `questions.yml` is baked in with `go:embed`.

```bash
agents-md-readiness -fail-under 0.8 https://github.com/apache/airflow
```

## Install it

[0.1.0](https://github.com/aarora79/jev-samples/releases/tag/agents-md-readiness/0.1.0) carries binaries for linux and macOS on amd64 and arm64, plus windows amd64:

```bash
curl -fsSL https://raw.githubusercontent.com/aarora79/jev-samples/main/samples/agents-md-readiness/go/install.sh | sh
```

The script reads the newest tag prefixed `agents-md-readiness/`, downloads the asset for your platform, checks it against the published `SHA256SUMS`, and installs to `/usr/local/bin` when that is writable or `~/.local/bin` when it is not. `VERSION` pins a tag, `BINDIR` picks the directory. It covers linux and darwin on amd64 and arm64, and it tells you to build from source on anything else.

Piping a script from the internet into a shell is a choice. Download it, read it, then run it:

```bash
curl -fsSLO https://raw.githubusercontent.com/aarora79/jev-samples/main/samples/agents-md-readiness/go/install.sh
less install.sh && sh install.sh
```

## Build it

Go 1.25 or newer, and no other tool:

```bash
cd samples/agents-md-readiness/go
go test ./...
go build -o agents-md-readiness .
./agents-md-readiness -version
```

`build.sh` cross-compiles every platform `install.sh` knows about from whichever machine you run it on, and writes `SHA256SUMS` beside the binaries:

```bash
./build.sh 0.1.0
```

That produced five binaries on 19 September 2026, 6.4M to 7.2M each, in 4.4 seconds on this machine: linux amd64 and arm64, darwin amd64 and arm64, windows amd64. `-trimpath` drops the build paths, and Go stamps the git revision and a dirty flag into the binary, so the same commit in a clean tree reproduces the same hashes and a dirty tree does not. `go version -m <binary>` prints both. The release assets came from a fresh clone of the tagged commit, which is why their hashes differ from a build in a working tree that has edits.

## Run it

Export the key, or let the binary find it. It reads `TYPESAFE_API_KEY` from the environment first, then from a `.env` file in the working directory or up to five parents above it, and it logs the path it read rather than the key.

```bash
agents-md-readiness                                    # ./AGENTS.md, then ./CLAUDE.md
agents-md-readiness path/to/CLAUDE.md                  # any local file
agents-md-readiness https://github.com/apache/airflow  # a repo root, both names tried
agents-md-readiness -fail-under 0.8 AGENTS.md          # exit 2 under the bar
agents-md-readiness -fail-on-credential AGENTS.md      # exit 2 on a leaked key
agents-md-readiness -json data AGENTS.md               # write the report
agents-md-readiness -questions ours.yml AGENTS.md      # your own payload
```

A run against `apache/airflow` on 19 September 2026, cut to the last rows:

```
AGENTS.md  (https://raw.githubusercontent.com/apache/airflow/HEAD/AGENTS.md)

| Key                 | Label                  | Type            | Jev returned              | Weight | Credit | Judgement      |
| ------------------- | ---------------------- | --------------- | ------------------------- | ------ | ------ | -------------- |
| `nested_files`      | nested file precedence | noul            | 0.04                      | 0.02   | 0.04   | missing        |
| `boundaries`        | boundaries and asks    | noul            | 0.99                      | 0.06   | 0.99   | strong         |
| `runnable_commands` | commands are runnable  | noul            | 0.95                      | 0.07   | 0.95   | strong         |
| `leaks_secret`      | leaks a credential     | noul (inverted) | 0.04                      | 0.15   | 0.96   | clean          |

**Readiness 0.94 / 1.00**, the weighted average of the credit column over 16 questions carrying 1.00 of weight.
Weights live in questions.yml, so raise the one you care about and re-run.

17 questions, 6,410 input tokens, 355 ms, $0.00027 at $0.042 per million input tokens.
```

The printed blocks, the table and the judgement words match the Python sample, and the JSON report carries the same keys, so the `jq` lines in the sample README work against either. Two differences on purpose: the Python writes a report on every run, where the binary writes one when `-json` names a directory, and each column pads to its own widest cell, so two runs of the same file can differ in table width.

## The deadband

Jev is a statistical model, so the same question about the same document comes back with a slightly different number each time. A credit of 0.851 and a credit of 0.849 straddle the 0.85 floor between `adequate` and `strong`, and a reader who ran the tool twice would see a different word each time.

`bandWord` in `score.go` names both bands when a value sits within 0.02 of an edge, so that credit reads `adequate to strong` on both runs. The same 0.02 widens the two unsure markers: a Choice or a Score flags unsure under 0.52 confidence, and a Noul flags it from 0.33 to 0.67.

Gate on the numbers rather than on the words. `-fail-under` compares readiness, and `-fail-on-credential` matches the judgement by prefix so a compound reading such as `likely present to suspect` still fires.

The deadband came from the Python sample, and the two implementations agree exactly: a sweep of 1,001 values through all three band sets, and 6,006 judgement cases across a Noul, an inverted Noul, three Score levels and a Choice, produce identical strings in both.

## Gate a pull request

| Flag | Effect |
| --- | --- |
| `-fail-under 0.8` | exit 2 when readiness falls under 0.80, and exit 2 when the repo holds neither file |
| `-fail-on-credential` | exit 2 when `leaks a credential` reads suspect or worse |
| `-json <dir>` | write `<owner>-<repo>-<file>.json` into that directory |
| `-questions <file>` | read the payload from your file instead of the baked-in copy |
| `-verbose` | print the raw JSON the API returned |

Exit codes: 0 scored, 1 error, 2 gate failed. A usage error and a thin AGENTS.md give different codes, so a job can tell them apart.

```yaml
- name: Score AGENTS.md
  run: |
    curl -fsSL https://raw.githubusercontent.com/aarora79/jev-samples/main/samples/agents-md-readiness/go/install.sh | sh
    agents-md-readiness -fail-under 0.8 -fail-on-credential AGENTS.md
  env:
    TYPESAFE_API_KEY: ${{ secrets.TYPESAFE_API_KEY }}
```

One call costs 6,410 input tokens against Airflow's file, $0.00027 at TypeSafe's September 2026 price of $0.042 per million input tokens.

## Where the payload lives

`../questions.yml` is canonical. `go:embed` cannot reach outside its own package, so this folder holds a copy: `build.sh` refreshes it before every build, and `payload_test.go` fails the moment the two diverge.

```
$ go test ./...
--- FAIL: TestEmbeddedPayloadMatchesPython (0.00s)
    payload_test.go:29: ../questions.yml and the embedded copy differ: run ./build.sh, which copies the canonical file in
FAIL
```

Edit the Python sample's `questions.yml` and rebuild. Edit the copy here and the test stops you.

`-questions ours.yml` overrides the embedded payload at runtime, which is how you score against your own questions and weights without rebuilding. A one-question file scored this repo's own AGENTS.md at `test commands` 0.62 in 319 ms.

## What the code is

Seven files, 1,991 lines, one dependency. `gopkg.in/yaml.v3` reads the payload; the Jev call is a `net/http` POST to `https://api.typesafe.ai/v1/systemone` with a Bearer token, so no SDK ships here. Roughly half those lines are comments: the files explain the Go machinery as they go, so a reader who works in Python can follow the port.

| File | Job |
| --- | --- |
| `main.go` | flags, exit codes, the gates |
| `payload.go` | reads `questions.yml`, embedded or named, in file order |
| `fetch.go` | resolves a path, a repo root or a file page; finds the key |
| `score.go` | the API call, credit per answer, readiness, judgement words |
| `output.go` | the explained answers and the padded markdown table |
| `report.go` | the JSON report and its filename |
| `payload_test.go` | the drift check against the canonical payload |

## Cut a release

Build from a clean clone of the commit you are tagging, so the revision Go stamps into each binary matches the release:

```bash
git clone --depth 1 https://github.com/aarora79/jev-samples.git /tmp/relbuild
cd /tmp/relbuild/samples/agents-md-readiness/go
./build.sh 0.1.0
gh release create agents-md-readiness/0.1.0 dist/* \
  --repo aarora79/jev-samples \
  --target "$(git rev-parse HEAD)" \
  --title "agents-md-readiness 0.1.0" \
  --notes "Static binaries for linux, macOS and Windows."
```

Versions are plain [semver](https://semver.org), `0.1.0` rather than `v0.1.0`, in the tag, the asset names and what `-version` prints. The tag carries the `agents-md-readiness/` prefix because `install.sh` resolves the newest tag with that prefix, which leaves room for another sample to ship its own binary. `dist/` is gitignored: the release holds the binaries and the repo holds the source.

0.1.0 came out of `3bfae70` on 19 September 2026, built in a clean clone, and every asset stamps that revision with `vcs.modified=false`. The install path ran end to end from an empty directory: `install.sh` resolved 0.1.0 off the tag, downloaded the linux amd64 asset, printed `checksum ok`, and installed a binary that reports `agents-md-readiness 0.1.0`. It then scored `langchain-ai/langchain` in 385 ms for 5,723 input tokens and exited 2 on `microsoft/vscode` under `-fail-under 0.8`.

## What to notice

- **The gate belongs next to the action.** `-fail-under` guards a merge and `-fail-on-credential` guards a leak, and the two carry different thresholds because being wrong costs different amounts.
- **The binary and the Python agree within run-to-run drift.** Against `anthropics/anthropic-cookbook` on 19 September 2026 the binary scored 0.9158 where the committed Python report holds 0.9131, and repeat Python runs move by that much on their own.
- **Two implementations of one judgement is the real cost here.** The payload stays single-source and the test enforces it, so what duplicates is the arithmetic and the layout. Change the scoring in Python and this port needs the same change by hand.
- **macOS binaries are unsigned.** Gatekeeper quarantines a `curl` download, so a first run needs `xattr -d com.apple.quarantine`, or `System Settings > Privacy & Security > Open Anyway`.
- **linux/amd64 is the platform I ran.** The other four cross-compile from this machine and nobody has run them yet.
