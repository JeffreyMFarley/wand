# wand — Design Document

> Language-agnostic API mocking infrastructure with agentic fixture management.

## Table of Contents

1. [Origin and Motivation](#origin-and-motivation)
2. [Core Insight](#core-insight)
3. [Architecture Overview](#architecture-overview)
4. [The Four Modes](#the-four-modes)
5. [Normalization](#normalization)
6. [Hooking Into Services](#hooking-into-services)
7. [The Fixture Store](#the-fixture-store)
8. [Claude Integration Points](#claude-integration-points)
9. [CLI Command Surface](#cli-command-surface)
10. [Repository Structure](#repository-structure)
11. [Configuration — wand.yaml](#configuration--wandyaml)
12. [Service Config Schema](#service-config-schema)
13. [Stability and Versioning](#stability-and-versioning)
14. [Migration Path from wand-python](#migration-path-from-wand-python)
15. [Known Hard Problems](#known-hard-problems)
16. [Open Questions](#open-questions)

---

## Origin and Motivation

`wand` grew out of a pattern first documented for Elasticsearch testing (2018) and later
evolved into a Python library.

The original Python implementation (`mocking.py`, `mockflake.py`, `mockhermes.py`)
works by intercepting calls at the Python process level using `monkeypatch` and
`unittest.mock`. This works well for Python but has fundamental limits:

- **Language-locked.** Every new language (Node, Go, Java) needs a full
  reimplementation of all mode logic, normalization, and fixture handling.
- **Logic is scattered.** Mode dispatch, normalization rules, and fixture I/O are
  spread across multiple files. Adding a new service means touching many places.
- **Manual ritual.** Developers must hold the full capture workflow in their heads:
  set the right env var, delete stale fixtures, run the right tests, verify output.
  Each step fails silently.

`wand` solves this by moving the interception point from inside the process to the
network layer, and by adding a Claude-powered CLI that owns the capture ritual.

---

## Core Insight

**The interception point moves from the Python layer to the network layer.**

Instead of monkeypatching inside a test process, the application talks to a local
proxy. The proxy is where all mode logic lives. The application — in any language —
never changes except to point its connection at `localhost`.

```
Before (Python only):
  test process → monkeypatch → mockflake.py → fixture files

After (any language):
  test process → shim (thin) → wand proxy → fixture files
                                           ↘ live service (when needed)
```

The shim's only job is protocol translation: serialize the native call into an HTTP
POST to the proxy, deserialize the response. No mode logic. No fixture loading. No
normalization. All of that lives in the proxy, once, in one language.

---

## Architecture Overview

Three tiers:

```
┌─────────────────────────────────────────────────────────┐
│  Test process  (any language)                           │
│  ┌──────────────┐  ┌─────────────────┐  ┌───────────┐  │
│  │ HTTP client  │  │ SQL driver shim │  │  gRPC     │  │
│  │              │  │                 │  │ intercept │  │
│  └──────┬───────┘  └────────┬────────┘  └─────┬─────┘  │
└─────────┼────────────────────┼─────────────────┼────────┘
          │  all routed to localhost proxy        │
          ▼                    ▼                  ▼
┌──────────────────────────────────────────────────┐  ┌──────────────────┐
│  wand proxy                                      │  │  Fixture store   │
│  ┌────────────┐  ┌──────────┐  ┌─────────────┐  │◄►│  __fixtures__/   │
│  │ Normalizer │→ │  Router  │→ │   Claude    │  │  │  index.json      │
│  └────────────┘  └──────────┘  └─────────────┘  │  └──────────────────┘
│   ci │ capture │ passthrough │ livetest           │
└──────────────────────────────┬───────────────────┘
                                │ passthrough / capture / livetest only
                                ▼
┌─────────────────────────────────────────────────────────┐
│  Live services                                          │
│  ┌──────────────┐  ┌─────────────────┐  ┌───────────┐  │
│  │  Snowflake   │  │  Hermes / gRPC  │  │ HTTP APIs │  │
│  └──────────────┘  └─────────────────┘  └───────────┘  │
└─────────────────────────────────────────────────────────┘
```

**Key properties:**
- The proxy is stateless. All state lives in the fixture store (just files on disk).
- In `ci` mode the proxy never makes a network call and needs no credentials.
- The fixture store is the repo's `__fixtures__/` directory — committed to source
  control, readable by any CI environment that can clone the repo.

---

## The Four Modes

Mode is set via the `WAND_MODE` environment variable. Default is `ci`.

### `ci`

The mode that runs in CI. No live service access, no credentials required.

1. Incoming request is normalized (noise fields stripped, versions neutralized).
2. Request is hashed (BLAKE2b, 16-byte digest → 32-char hex, hyphen-segmented).
3. Proxy looks up `__fixtures__/<service>/<hash>_req.*` + `<hash>_resp.*`.
4. **Miss = hard fail.** The error message includes the normalized request and hash
   so the developer knows exactly why there was a miss. Claude can suggest whether
   a missing normalization rule is the likely cause.
5. Hit → return stored response to the test process. No network call made.

### `capture`

Run once by a developer with live service access to create or refresh fixtures.

1. Request is forwarded to the live service with real credentials.
2. Live response is received and normalized (noise fields stripped).
3. Request + response pair is written to `__fixtures__/<service>/` as
   `<hash>_req.*` and `<hash>_resp.*`.
4. Claude generates a human-readable scenario name and updates `index.json`.
5. Normalized live response is returned to the test process (test continues normally).

### `passthrough`

Transparent mode. The proxy is invisible — everything passes through to live services
and nothing is written. Used during initial development before fixtures exist.

1. Request is forwarded to the live service unchanged.
2. Live response is returned unchanged.
3. Nothing written to disk.

### `livetest`

Contract testing mode. Verifies that stored fixtures still match live service behavior.

1. Request is forwarded to the live service (normalized copy also hashed).
2. Live response is received and normalized.
3. Stored fixture for the same hash is loaded.
4. Live response is diffed against stored response.
5. Claude classifies the diff: **breaking** (schema change, missing fields),
   **benign** (additive — new optional fields), or **noise** (timestamps, durations,
   cursor values that should be in the normalization config).
6. Classification determines CI outcome: breaking → fail, benign → warn, noise →
   suggest normalization config update.

---

## Normalization

Normalization is the most critical correctness concern in the proxy. It runs in two
places and must be symmetric:

- **On the request** — before hashing, so the hash is stable across calls that
  differ only in non-semantic fields (session IDs, request timestamps).
- **On the response** — before writing to disk during capture, so stored fixtures
  don't contain ephemeral values that would never match on replay.

### Three categories of normalization

| Category | What it is | Example |
|---|---|---|
| **Noise removal** | Fields that are real but meaningless for matching | `took`, `timed_out`, `_shards` (ES); `queryId`, `statementStatusUrl` (Snowflake) |
| **Version neutralization** | Fields whose values encode version info that changes independently of behavior | `sales-v1` / `sales-v2` index names (ES); `DB_V2.SCHEMA` (Snowflake) |
| **Sentinel substitution** | Ephemeral cursors / tokens that are meaningless outside the original session | `_scroll_id` → `fake-scroll-id`; `sessionId` → `wand-fake-session` |

### Config format

Normalization is declared per-service in YAML. The `request` and `response` sections
are applied independently — they are separate passes even though they share the same
config file.

```yaml
# wand/services/snowflake.yaml
normalization:
  request:
    remove_fields:
      - sessionId
      - requestId
    patterns:
      - description: "Normalize inline timestamps"
        location: sql
        transform: normalize_timestamps   # replaces CURRENT_TIMESTAMP with ?

  response:
    remove_fields:
      - queryId
      - statementStatusUrl
      - createdOn
      - took
    sentinels:
      - field: sessionId
        value: wand-fake-session-00000000
    patterns:
      - description: "Neutralize database version suffix"
        location: "$.results[*].dbName"
        transform: strip_version_suffix   # DB_V2 → DB
```

### Claude-assisted normalization discovery

On a fresh service, the normalization rules aren't known in advance. The discovery
workflow:

1. Run two captures of the same test scenario.
2. `wand diff` compares the two raw fixture pairs.
3. Claude identifies fields that changed between the two captures of the same
   scenario — those are by definition candidates for normalization.
4. Claude proposes additions to the service's normalization config.
5. Developer reviews and approves. Config is committed alongside fixtures.

Claude also detects over-normalization: if a field being removed has different values
across different test scenarios, removing it would cause fixture collisions (two
different scenarios would hash to the same fixture). This must be surfaced as an error,
not silently applied.

---

## Hooking Into Services

The shim's only job is protocol translation. All mode logic stays in the proxy.

### HTTP APIs — no shim needed

Set environment variables before the test process starts:

```bash
HTTP_PROXY=http://localhost:8877
HTTPS_PROXY=http://localhost:8877
```

Every HTTP client in every language respects these by convention. For HTTPS, the proxy
presents a self-signed cert; the test environment must trust it (standard
mitmproxy-style setup). Zero application code changes required.

### Snowflake — connection string redirect

Override the account/host env vars so the Snowflake client connects to the proxy
instead of the live cluster:

```bash
SNOWFLAKE_ACCOUNT=localhost
SNOWFLAKE_PORT=8878
```

For the existing Python `mockflake.py` shim, the migration is: keep the current
interface, replace all fixture logic with a single HTTP POST to the proxy:

```python
# mockflake.py after migration — thin shim only
WAND_PROXY = os.environ.get("WAND_PROXY", "http://localhost:8877")

class Mockflake:
    def execute(self, query, params=None):
        resp = requests.post(f"{WAND_PROXY}/snowflake", json={
            "query": query,
            "params": params
        })
        return resp.json()
```

The proxy speaks enough of the Snowflake wire protocol to complete the auth handshake
(see [Handshake Handling](#known-hard-problems) below).

### gRPC / Hermes — client interceptor

Register a client interceptor at channel construction time. The interceptor serializes
the proto request, POSTs to the proxy, deserializes the response. Application code
above the interceptor is unchanged.

```python
# Python gRPC
class WandInterceptor(grpc.UnaryUnaryClientInterceptor):
    def intercept_unary_unary(self, continuation, client_call_details, request):
        resp = requests.post(
            f"{os.environ['WAND_PROXY']}/grpc",
            json={
                "method": client_call_details.method,
                "request": MessageToDict(request)
            }
        )
        return DictToMessage(resp.json(), type(request))

channel = grpc.intercept_channel(
    grpc.insecure_channel("hermes:50051"),
    WandInterceptor()
)
```

Streaming calls (`StreamClientInterceptor`) need a separate implementation — see
[Known Hard Problems](#known-hard-problems).

### Decision rule

| Protocol | Hook point | App code changes |
|---|---|---|
| HTTP / HTTPS | `HTTP_PROXY` env var | None |
| SQL (Snowflake, Postgres, etc.) | Connection string / account env var | None if injectable; monkeypatch shim if not |
| gRPC | Client interceptor registered at channel init | Channel construction only |
| Other | Protocol shim translating to HTTP POST | Thin shim at client construction |

---

## The Fixture Store

### File naming

Fixtures use BLAKE2b content-addressed naming (16-byte digest, hyphen-segmented into
eight 4-char groups):

```
__fixtures__/
  snowflake/
    0b2d-c84a-bed5-1060-0a97-4328-ed90-42db_req.jsonl
    0b2d-c84a-bed5-1060-0a97-4328-ed90-42db_resp.jsonl
  hermes/
    1a3f-22bc-...._req.jsonl
    1a3f-22bc-...._resp.jsonl
```

In the v1 format every fixture — request and response, for every service — is stored
as JSON Lines (`.jsonl`): a one-line metadata header followed by the one-line JSON body. This replaces the
per-protocol on-disk formats used by the original `msmt` library (`.sql` for Snowflake
requests, `.df` parquet for responses, `.pbb` protobuf for Hermes). The proxy is
language-agnostic, so it speaks one wire format — JSON — and each shim is responsible
for translating its native payload to and from JSON before it reaches the store.

The hash is computed from the **normalized** request. This means:
- The same logical request always maps to the same fixture regardless of noise fields.
- Two different logical requests (different SQL, different parameters) always map to
  different fixtures.

### index.json

The index is the semantic layer over the content-addressed store. It maps hashes to
human-readable scenario metadata. Claude writes to it during `capture`. Humans read it
during debugging.

```json
{
  "0b2d-c84a-bed5-1060-0a97-4328-ed90-42db": {
    "scenario": "books search, authors=King, sales index",
    "service": "snowflake",
    "captured": "2025-11-14",
    "captured_by": "wand/1.0.0",
    "tests": ["test_report.py::test_authors_search"],
    "request_summary": "SELECT ... FROM sales WHERE author ILIKE 'King%'"
  }
}
```

### Fixture file header

Every fixture file begins with a one-line metadata header:

```json
{"wand_version": "1", "service": "snowflake", "captured": "2025-11-14"}
```

The proxy checks this on read and fails clearly if the version is incompatible, rather
than silently mismatching.

### Reachability and `tidy`

Content-addressing makes capture **purely additive**: the hash is derived from the
normalized request, so when a code change alters a request shape it produces a *new*
hash and a *new* fixture. The old fixture is never overwritten — it is simply never
requested again. Left alone, the store grows monotonically, accumulating orphans every
time a request changes. This is the same shape as git objects: adding is cheap and
additive, and the only way to reclaim dead ones is a separate reachability sweep.

`wand tidy` is that sweep, modeled on `git gc` rather than on delete-everything:

- **Mark.** Every code path that serves a fixture appends the fixture it touched to a
  run-local log, `__fixtures__/access.jsonl` (one compact JSON object per line —
  `{"service","hash","missing"?}` — mirroring the divergence log). Both request paths
  record: the Go proxy (`ci` hits/misses, `capture` writes) *and* the boto3 shim, which
  replays from disk without ever touching the proxy. A `missing:true` entry marks a
  `ci` miss.
- **Sweep.** `wand tidy` loads the log, computes the set of reached fixtures, and reports
  every stored fixture the last run did *not* touch. It runs against the committed store
  in `ci` mode, so **no live credentials are needed** — the sweep is pure replay.

Why this beats the old "delete all fixtures, then re-capture everything" ritual: that
ritual needed live creds for every service and a green full-suite *live* run just to
clean up, and it destroyed the curated `scenario`/`tests` metadata in `index.json`.
`tidy` needs no creds, and preserves the metadata of every fixture still in use.

**Safety model.** The only real hazard is a *partial* mark run: if the recorded run
exercised a subset of tests, fixtures for the un-run tests look unreached and would be
wrongly deleted. Three properties guard against this:

- **Dry-run by default.** Plain `wand tidy` only lists orphans; deletion requires an
  explicit `--force`.
- **A human-verified full, passing run.** The developer runs the whole suite green
  before sweeping — the tool cannot see the test runner's exit code, so this is the
  load-bearing guarantee, and the CLI states it at every step. `--reset` clears the log
  so the marking run starts from a clean slate (stale marks from before a code change
  would otherwise keep dead fixtures alive).
- **Misses are a hint, not a blocker.** An early design refused to delete whenever a
  miss was recorded, on the theory that a miss meant an aborted run. Real usage
  disproved it: application code routinely makes best-effort API calls and *catches* the
  absence, so misses coexist with a perfectly green suite — blocking on them made `tidy`
  unusable for such projects. And a miss never detected the actual danger anyway, since a
  partial run produces *zero* misses. So misses are now reported as a coverage hint
  ("these calls have no stored fixture — capture them if they matter") and never gate
  deletion.

After a successful `--force`, the deleted fixtures' `index.json` entries are dropped and
the access log is cleared, so the next tidy cycle starts fresh.

---

## Claude Integration Points

Claude is called for semantic work — things that require understanding intent, not
mechanical execution. The proxy handles all mechanical work without Claude.

| Command | What Claude does |
|---|---|
| `wand capture "<description>"` | Reads git diff + `index.json`, identifies which tests exercise the changed code paths, returns scoped test IDs for developer confirmation before any capture runs |
| `wand capture` (post-capture) | Generates human-readable scenario name from the request/response pair, updates `index.json` |
| `wand diff` | Reads before/after fixture pairs, produces plain-English summary of what changed (new SQL clauses, response schema changes, etc.) suitable for PR description |
| `wand doctor` | After `livetest`, classifies each divergence as **breaking**, **benign**, or **noise**. Breaking = fail CI. Benign = warn. Noise = suggest normalization config update |
| `wand explain <hash>` | Returns human-readable description of what scenario the fixture covers, what the key parameters were, and which tests use it |
| `wand scaffold <file-or-dir>...` | Reads each given source file (directories are walked to source files) and generates an integration test for it, placed by language convention (Go beside source, Python mirrored under tests/, JS/TS beside source, PHP beside source, Ruby mirrored under spec/, Java mirrored from src/main/java into src/test/java); non-qualifying files are skipped |
| CI miss explanation | When `ci` mode fails to find a fixture, Claude inspects the normalized request and suggests whether a missing normalization rule is the likely cause |

### The god-function rule

`scaffold` qualifies any function or method over **400 lines automatically** —
length alone, with no requirement that a qualifying external call be found in it.
This is deliberate, and it is not really about that one function's correctness.

The premise is behavioral: a developer who lacked the discipline to split a
1200-line function did not write tests for it either, so a god function is a
reliable marker for *exactly where coverage is missing*. Requiring the scanner to
first locate an external call buried under fifty variables and deep nesting just
reintroduces the failure it is trying to catch — the call is missed, the file is
disqualified, and the least-tested code stays untested. Length is the signal we
can detect without that risk.

The generated test is intentionally vague: it exercises the whole function
end-to-end and asserts **only on the final return value** (see the god-function
step in the scaffold prompt). It is a characterization pin, not a correctness
check — it keeps passing while the interior is torn apart, which is the point.
Its real payoff is the **fixtures captured downstream** from whatever calls the
function makes: in practice one 1200-line function yielded ~200 fixtures, and only
once those existed was it safe to refactor fifty positional args into a dict input
and break the body into modular functions.

One consequence of dropping the external-call condition: a god function that
reaches *nothing* external still qualifies. It gets the return-value pin (a real
refactoring net) but produces no fixtures — the fixture payoff lands only when
there is a call buried somewhere inside. That trade is accepted on purpose: a
harmless empty-fixture test on a pure god function is far cheaper than silently
skipping a god function whose call the scanner couldn't find.

### Claude API usage

All Claude calls use `claude-sonnet-4-6`. API key comes from `ANTHROPIC_API_KEY`
environment variable. The CLI makes calls directly — no server needed.

Claude calls are **not** in the hot path for `ci` mode. `ci` mode is fully
deterministic and makes no Claude calls. Claude is only called from developer-facing
CLI commands.

---

## CLI Command Surface

```
wand init                              Scaffold wand.yaml for this project
wand proxy start                       Start the proxy sidecar
wand proxy stop                        Stop the proxy sidecar
wand proxy status                      Show mode, port, loaded service configs

wand capture <file-or-dir>...          Explicit scope: test files, or dirs walked to test files
wand capture --from-diff [description]  Agentic escape hatch: Claude infers scope from the git diff
wand capture --name                    Name captured fixtures (post-capture, Claude)
wand diff                              Semantic diff of changed fixtures (git diff)
wand diff --pr <number>                Semantic diff of fixtures changed in a PR
wand doctor                            livetest all fixtures, classify divergences
wand tidy [--force] [--reset]          Delete fixtures unreached by the last ci-mode run
wand verify                            ci mode dry-run, report any misses
wand explain <hash>                    What scenario does this fixture cover?
wand scaffold <file-or-dir>...         Generate integration tests for the given source files/dirs

wand normalize discover                Run two captures, propose normalization config
wand normalize check                   Detect over-normalization risks (collision check)
```

### Design principles for the CLI

- Commands split into **plumbing** (`proxy start/stop/status`, `verify`, `tidy`) and
  **agentic** (`capture`, `diff`, `doctor`, `scaffold`, `explain`).
- Plumbing commands work without Claude and without an API key.
- Agentic commands always show their proposed scope to the developer and ask for
  confirmation before any destructive action (writing fixtures, deleting fixtures).
- `wand capture "<description>"` never captures blindly across all tests. Scope creep
  in fixture capture produces large unreviable commits.
- Errors from `ci` mode are always actionable: normalized request, computed hash, and
  Claude's best guess at the cause.

---

## Repository Structure

```
wand/
├── cmd/
│   ├── wand/               Main CLI binary entry point
│   └── wand-proxy/         Proxy server (runnable standalone, also embedded in CLI)
├── proxy/
│   ├── router.go           Mode dispatch (ci / capture / passthrough / livetest)
│   ├── normalizer.go       Config-driven request + response normalization
│   ├── store.go            Fixture read / write / index management
│   ├── hasher.go           BLAKE2b request hashing
│   ├── handshake.go        Protocol handshake faking (Snowflake auth, etc.)
│   └── claude.go           All Claude API calls
├── services/               Built-in service normalization configs
│   ├── snowflake.yaml
│   ├── elasticsearch.yaml
│   ├── hermes.yaml
│   └── http.yaml           Generic HTTP (minimal normalization)
├── shims/                  Thin per-language protocol shims
│   ├── python/
│   │   ├── mockflake.py    Replacement for msmt's mockflake — thin proxy redirect
│   │   └── mockhermes.py   Replacement for msmt's mockhermes
│   ├── node/
│   │   └── snowflake.js
│   └── go/
│       └── grpc.go
├── examples/               Runnable examples — these are load-bearing (CI tests them)
│   ├── elasticsearch/      Original blog post scenario in Python
│   ├── node-postgres/      SQL from Node.js
│   └── go-grpc/            gRPC from Go
└── docs/
    ├── hooking.md          Per-protocol hookup instructions
    ├── normalization.md    Normalization config reference
    ├── claude-integration.md  What Claude does and when
    └── migration.md        Step-by-step migration from wand-python
```

### Examples are first-class

Each example in `examples/` is a minimal application with a real test suite that must
pass in `ci` mode. They are run in CI on every commit to `wand`. If an example breaks,
the proxy broke. They are not documentation — they are integration tests for the proxy.

---

## Configuration — wand.yaml

Lives at the project root. Generated by `wand init`.

```yaml
proxy:
  port: 8877
  mode: ${WAND_MODE:-ci}          # env var override with default

services:
  snowflake:
    config: wand/services/snowflake.yaml   # can be built-in or project-local
    shim: python                           # which shim package to use
  hermes:
    config: wand/services/hermes.yaml
    shim: grpc-python

fixtures:
  path: tests/__fixtures__
  index: tests/__fixtures__/index.json

claude:
  model: claude-sonnet-4-6
  # api key read from ANTHROPIC_API_KEY
```

### Environment variable matrix

This table is the source of truth for CI configuration. It replaces the equivalent
table in the `msmt` README.

| Variable | Value | Live services? | Notes |
|---|---|---|---|
| `WAND_MODE` | `ci` | No | Default. Fixture replay only. |
| `WAND_MODE` | `capture` | Yes | Records request/response pairs. |
| `WAND_MODE` | `passthrough` | Yes | Transparent. Nothing written. |
| `WAND_MODE` | `livetest` | Yes | Compares live responses to stored fixtures. |
| `ANTHROPIC_API_KEY` | _(set)_ | — | Required for agentic CLI commands only. Not needed for `ci` mode. |
| `WAND_PROXY` | `http://localhost:8877` | — | Used by shims to locate the proxy. Set automatically by `wand proxy start`. |

---

## Service Config Schema

Full schema for a service normalization config file.

```yaml
# Metadata
name: snowflake
version: "1"

# Handshake: how to handle connection setup before query traffic begins
handshake:
  fake_session_token: "wand-fake-session-00000000"
  intercept_from: query   # start matching fixtures at this message type

# Normalization rules — applied separately to request and response
normalization:
  request:
    remove_fields:          # top-level field names to delete before hashing
      - sessionId
      - requestId
    patterns:               # JSONPath-targeted transforms
      - description: "Normalize inline timestamps in SQL"
        location: sql
        transform: normalize_timestamps

  response:
    remove_fields:
      - queryId
      - statementStatusUrl
      - createdOn
      - took
    sentinels:              # replace with a known constant value
      - field: _scroll_id
        value: fake-scroll-id
      - field: sessionId
        value: wand-fake-session-00000000
    patterns:
      - description: "Strip version suffix from index/db names"
        location: "$.hits.hits[*]._index"
        transform: strip_version_suffix

# Built-in transforms available
# normalize_timestamps  — replaces CURRENT_TIMESTAMP, NOW(), date literals
# strip_version_suffix  — removes trailing -v1, -v2, _V1, _V2 etc.
# redact_pii            — replaces email, SSN, phone patterns with placeholders
```

---

## Stability and Versioning

`wand` defines its own **v1 fixture format** (JSON header + JSON body, BLAKE2b 16-byte
hash naming) rather than adopting the original `msmt` on-disk formats byte-for-byte.
The `msmt` fixtures (`.sql` / `.df` parquet / `.pbb` protobuf, hashed over the Python
function name plus stringified arguments) are **not** read directly. They are
regenerated through a one-time `capture` pass against the live services, which writes
them out in the v1 JSON format. See [Migration Path](#migration-path-from-wand-python).

> **Why the break from `msmt`.** `msmt` hashed over `funcname + str(args)` — a
> Python-call identity that only a Python process can reproduce. A language-agnostic
> proxy cannot reconstruct that input from an HTTP request, and its parquet/protobuf
> response bodies are Python-runtime artifacts. Carrying those forward would have
> re-coupled the proxy to Python, defeating the core insight. Regeneration is a
> one-time cost paid once per service by a developer with live access.

Once v1 is in use, stability rules apply going forward:
- The fixture file format (header + body) is versioned. `wand_version: "1"` is the
  initial version.
- The proxy reads the version header and fails clearly if incompatible, rather than
  silently mismatching or corrupting.
- The hash algorithm (BLAKE2b, 16-byte digest, rendered as eight hyphen-separated
  4-char hex groups) is frozen. Changing it would invalidate every v1 fixture — this
  must never happen in a minor version.
- `index.json` is additive. Older entries without new fields are valid; missing fields
  get zero values.
- The normalization config schema is additive. New transform types added in later
  versions are ignored by older proxy versions (with a warning, not a crash).

---

## Migration Path from wand-python

The proxy is designed so that existing `msmt` **tests** require no changes. The
migration is in the infrastructure layer, with one deliberate exception: fixtures are
**regenerated once** into the v1 JSON format (see
[Stability and Versioning](#stability-and-versioning) for why the old formats are not
read directly).

### Step 0 — Regenerate fixtures into v1 format

With live service access, run a `capture` pass to rewrite the existing
`__fixtures__/snowflake/` and `__fixtures__/hermes/` sets in the v1 JSON format. This
is a one-time cost per service. After this, `ci` mode runs offline as before. The
original `.sql` / `.df` / `.pbb` files can be deleted once the JSON fixtures are
committed and CI is green.

### Step 1 — Run proxy alongside existing system

Start `wand proxy start` in `passthrough` mode. No behavioral change. Verify it starts
and stops cleanly in your CI environment.

### Step 2 — Slim down mockflake.py

Replace fixture logic in `mockflake.py` with a redirect to the proxy (see shim in
`shims/python/mockflake.py`). The shim serializes the SQL call to JSON for the proxy;
the v1 fixtures written in Step 0 back it.

### Step 3 — Slim down mockhermes.py

Same treatment. The shim serializes the proto request to JSON; the regenerated v1
Hermes fixtures back it.

### Step 4 — Simplify mocking.py

`MockService` and `MockedFunction` lose all mode logic, fixture loading, and
normalization. They become thin wrappers that delegate to the shim. Most of
`mocking.py` moves into the proxy.

### Step 5 — Update conftest.py

The `mock_snowflake` and related fixtures just set `WAND_PROXY` and start the proxy
sidecar. The `MOCKFLAKE_MODE` env var is replaced by `WAND_MODE`.

### Step 6 — Generate index.json

Run `wand explain` across the regenerated fixtures to backfill `index.json`. This is
not required for `ci` mode to work — it's for developer experience.

### What does not change

- `__fixtures__/` directory structure and hash-based file naming (the hash algorithm
  is unchanged; only the payload encoding and file extension change)
- Test files (they still import `Mockflake`, still use the same fixture paths)
- CI configuration (same env var pattern, same `pytest` invocation)

### What does change

- Fixture bodies are re-encoded as JSON (with a version header) and the `_req` / `_resp`
  files become `.json`, regenerated once via `capture` (Step 0)

---

## Known Hard Problems

### 1. Protocol handshake faking

Database clients (Snowflake, Postgres) establish a connection and complete auth before
sending any queries. In `ci` mode there is no live service, so the proxy must fake the
handshake response convincingly enough that the client proceeds to send queries.

For Snowflake: the proxy returns a hardcoded fake session token during the auth
exchange, then begins fixture matching once SQL appears. The `handshake` section of the
service config declares this behavior explicitly.

For the Python shim, `mockflake.py` already handles this by mocking the connection
object with `self.conn = Mock()`. The shim migration keeps this behavior — the shim
fakes the connection, the proxy handles the queries.

### 2. Non-deterministic request fields

Some requests include fields that change on every call even though the semantic content
is identical: inline timestamps, session tokens, auto-generated request IDs. These must
be in the normalization config's `request.remove_fields` or `request.patterns` or the
hash will never match a stored fixture.

The normalization discovery workflow (`wand normalize discover`) exists specifically to
catch these. It runs two captures of the same scenario and diffs the raw requests.
Fields that differ between the two are candidates for normalization.

### 3. gRPC streaming calls

Unary gRPC calls (one request, one response) are handled by `UnaryUnaryClientInterceptor`.
Streaming calls (server-streaming, client-streaming, bidirectional) need
`StreamClientInterceptor` and the fixture format must accommodate sequences of messages,
not just a single request/response pair.

The initial implementation targets unary calls only. Hermes uses unary calls for all
current msmt integrations. Streaming support is deferred until a concrete need exists.

### 4. Multi-service transactions

A single test may call Snowflake twice and Hermes once, in a specific order. The proxy
handles each call independently against its own fixture. Order is not enforced at the
proxy level — if test correctness depends on call ordering, that is the test's
responsibility, not the proxy's.

If two calls to the same service with identical normalized requests must return
different responses (e.g. pagination), the fixture format needs sequence support. This
is not in the initial implementation. The current approach: ensure the normalized
requests are actually different (e.g. the pagination cursor is a semantic field, not
a noise field).

### 5. HTTPS interception

For HTTPS APIs, the proxy must present a TLS certificate the client trusts. This
requires either:
- A self-signed CA cert trusted by the test environment (standard mitmproxy approach)
- Disabling TLS verification in the test environment (simpler, but only acceptable in
  isolated test environments)

The proxy ships with a self-signed CA. `wand init` offers to install it into the
system trust store for development use.

---

## Open Questions

**Q: Should the proxy support a server mode for shared team infrastructure?**
Currently designed as a local sidecar. A shared proxy could reduce duplication of
fixture sets across developer machines, but adds operational complexity and a
single-point-of-failure in CI. Decision deferred.

**Q: How should merge conflicts in fixture files be resolved?**
Hash-named files rarely conflict (two branches would have to produce the same hash for
a different scenario). `index.json` is more conflict-prone. The resolution strategy
(ours / theirs / Claude-assisted merge) is not yet defined.

**Q: Should `wand.yaml` support per-environment fixture paths?**
Useful if you want a smaller fixture set for fast unit tests and a larger one for
integration tests. Not in initial implementation.

**Q: What is the right distribution mechanism?**
Options: GitHub Releases binary download, Homebrew formula, `go install`, Docker image.
The binary approach (GitHub Releases + install script) is simplest for CI adoption.
Homebrew is better for developer machines. Both are likely needed.

**Q: Should normalization transforms be extensible (user-defined)?**
Built-in transforms (`normalize_timestamps`, `strip_version_suffix`, `redact_pii`)
cover most cases. A plugin system for project-specific transforms adds complexity.
Current plan: if a built-in transform doesn't cover a need, add it to the built-ins
rather than building a plugin system.
