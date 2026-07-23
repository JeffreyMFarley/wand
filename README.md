# wand

**Language-agnostic API mocking for integration tests — with a Claude-powered CLI that owns the capture ritual.**

Your tests talk to a local proxy instead of to Snowflake, Elasticsearch, gRPC, or any HTTP API. The proxy records real responses once, then replays them forever. In CI there are no credentials, no network calls, and no flakes — just files on disk.

```
test process  →  thin shim  →  wand proxy  →  __fixtures__/   (replay in CI)
                                          ↘  live service     (only when capturing)
```

---

## Why this exists

The pattern started as a Python library (`mockflake`, `mockhermes`) that monkeypatched calls inside the test process. It worked, but every new language meant reimplementing all of the mode logic, normalization, and fixture handling from scratch — and developers had to carry the whole capture workflow in their heads.

`wand` moves the interception point from **inside the process** to **the network layer**. All the logic lives in one proxy, written once, in Go. Your application — in any language — changes nothing except where it points its connection. The shims are thin: they translate a native call into an HTTP POST and translate the response back. That's it.

For the full rationale, architecture, and design decisions, read **[DESIGN.md](DESIGN.md)**.

---

## The four modes

Mode is set with the `WAND_MODE` environment variable. Default is `ci`.

| Mode | Live services? | Credentials? | What it does |
|---|---|---|---|
| `ci` | No | No | **Default.** Replays stored fixtures. A miss is a hard failure with the normalized request + hash so you know exactly why. |
| `capture` | Yes | Yes | Forwards to the live service, normalizes, and writes the request/response pair to `__fixtures__/`. Claude names the scenario. |
| `passthrough` | Yes | Yes | Transparent. Everything hits the live service; nothing is written. |
| `livetest` | Yes | Yes | Diffs live responses against stored fixtures and classifies the drift (breaking / benign / noise). |

The key property: **`ci` mode is fully deterministic and makes no Claude calls and no network calls.** Claude is only invoked from developer-facing CLI commands.

---

## Getting started

```bash
# 1. Build the CLI
go build -o wand ./cmd/wand

# 2. Scaffold config + install shims for your project's languages
./wand init

# 3. Start the proxy sidecar
./wand proxy start

# 4. Point your tests at the proxy and run them in CI mode (the default)
WAND_MODE=ci pytest        # or: go test ./... , npm test, etc.
```

`wand init` writes a `wand.yaml`, detects your project's language, and drops the
appropriate shims into a tool-managed `.wand/` directory. For Python projects using
boto3, it also wires up a `conftest.py` so `WAND_MODE=capture pytest` produces fixtures
without any manual setup.

### Capturing fixtures for the first time

```bash
# With live credentials in your environment:
WAND_MODE=capture pytest tests/test_report.py

# Then let Claude name the scenarios and update the index:
./wand capture --name
```

Commit the resulting `__fixtures__/` directory. From then on, CI replays them with no
credentials and no network.

---

## CLI

```
wand init                        Scaffold wand.yaml and install shims for detected languages
wand proxy start|stop            Start or stop the proxy sidecar
wand capture <file-or-dir>...    Print the capture command for the given test files/dirs
wand capture --from-diff [desc]  Claude infers the capture scope from the git diff
wand capture --name              Name captured fixtures and update index.json (post-capture)
wand diff [--pr <number>]        Semantic diff of changed fixtures (Claude summary)
wand doctor                      livetest all fixtures and classify divergences
wand tidy [--force] [--reset]    Delete fixtures unreached by the last ci-mode run
wand verify                      ci-mode dry run; report any fixture misses
wand explain <hash>              Describe what scenario a fixture covers
wand scan <file-or-dir>...       Report which source files would get tests (generates nothing)
wand scaffold <file-or-dir>...   Generate integration tests for the given source files/dirs
wand normalizer                  Run normalization discovery/checks
wand help                        Show this help
```

Commands split into two families:

- **Plumbing** (`proxy`, `verify`, `tidy`) — work without Claude and without an API key.
- **Agentic** (`capture`, `diff`, `doctor`, `scaffold`, `explain`) — call Claude for the
  semantic work and always show you their proposed scope before writing or deleting anything.

Agentic commands need `ANTHROPIC_API_KEY` set. `ci` mode never does.

---

## How fixtures work

Fixtures are content-addressed. The proxy normalizes each request (strips noise fields,
neutralizes versions, substitutes ephemeral tokens), hashes the **normalized** request
with BLAKE2b, and stores the pair as JSON Lines:

```
__fixtures__/
  snowflake/
    0b2d-c84a-bed5-1060-0a97-4328-ed90-42db_req.jsonl
    0b2d-c84a-bed5-1060-0a97-4328-ed90-42db_resp.jsonl
  index.json          # human-readable scenario metadata, maintained by Claude
```

Because the hash comes from the normalized request:

- The same logical call always maps to the same fixture, regardless of session IDs,
  timestamps, or other noise.
- Two genuinely different calls always map to different fixtures.

Normalization is the heart of correctness here — see **[docs/normalization.md](docs/normalization.md)**.

---

## Hooking your service in

The shim's only job is protocol translation. Common cases:

| Protocol | Hook point | App code changes |
|---|---|---|
| HTTP / HTTPS | `HTTP_PROXY` / `HTTPS_PROXY` env var | None |
| SQL (Snowflake, Postgres) | Connection string / account env var | None if injectable; thin shim otherwise |
| gRPC | Client interceptor at channel construction | Channel construction only |
| Other | Thin shim translating to an HTTP POST | At client construction |

Details and copy-pasteable snippets live in **[docs/hooking.md](docs/hooking.md)**.

---

## Repository layout

```
wand/
├── cmd/
│   ├── wand/         CLI entry point
│   └── wand-proxy/   Standalone proxy server
├── proxy/            The proxy: routing, normalization, fixture store, hashing, Claude calls
├── services/         Built-in per-service normalization configs (snowflake, elasticsearch, http)
├── shims/            Thin per-language protocol shims (python, node, go)
├── examples/         Runnable examples — load-bearing; CI runs them on every commit
└── docs/             Hooking, normalization, and Claude-integration references
```

The **examples are first-class**: each is a minimal app with a real test suite that must
pass in `ci` mode, run in CI on every commit. If an example breaks, the proxy broke.

---

## Documentation

- **[DESIGN.md](DESIGN.md)** — the full design: motivation, architecture, modes, versioning, and known hard problems.
- **[docs/hooking.md](docs/hooking.md)** — per-protocol hookup instructions.
- **[docs/normalization.md](docs/normalization.md)** — normalization config reference.
- **[docs/claude-integration.md](docs/claude-integration.md)** — what Claude does and when.

---

## Status

`wand` is under active development. The proxy core, normalization, fixture store, and the
Python/boto3 shim path are the most mature; several CLI subcommands are still being filled
in. Check `wand help` for what's wired up in your build.
