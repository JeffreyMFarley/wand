# wand — Python shims

Thin Python client-side shims for the wand proxy. All mode logic, normalization,
hashing and fixture I/O live in the Go proxy; these just translate a native call
into wand's contract. Flat modules — copy the ones a project needs.

## Modules

| File | What it is | Depends on |
|---|---|---|
| `client.py` | `WandProxy` — a minimal HTTP client speaking the proxy contract (`X-Wand-Service` header, JSON body, 404 → `WandFixtureMiss`). Also `normalized_hash()`, which reproduces the proxy's BLAKE2b-128 request hash offline for hand-authoring/verifying fixtures. | `requests` |
| `boto3_shim.py` | boto3 ↔ wand bridge for read-only AWS calls. Intercepts `botocore.client.BaseClient._make_api_call` and, per `WAND_MODE`, replays (`ci`) or records (`capture`) responses as wand-format fixtures. Sidesteps SigV4/TLS and the XML-vs-JSON wire format by working on parsed dicts. | `botocore`, `client.py` |
| `marks.py` | `skip_on_fixture_miss` — a pytest decorator that turns a ci-mode `WandFixtureMiss` into a skip, so a suite stays green until fixtures are captured. | `pytest`, `client.py` |

`boto3_shim.py` and `marks.py` import `client` with a `try: from client / except: from .client` fallback, so they work whether dropped in as loose sibling modules (their directory on `sys.path`) or into a package.

The module is `boto3_shim.py`, **not** `boto3.py`, so it never shadows the real
`boto3` package.

## boto3 bridge — usage

Capture (live, with real read-only credentials), then replay offline:

```python
from boto3_shim import intercept
import boto3

# WAND_MODE=capture → live call + record; default ci → replay from __fixtures__/
with intercept():
    ec2 = boto3.client("ec2", region_name="us-east-1")
    ec2.describe_instances()          # recorded, or served from a fixture
```

Fixtures land under `WAND_FIXTURES` (default `__fixtures__/`) as
`<service>/<hash>_req.json` + `_resp.json`, with an `index.json` entry — the same
store and format the Go proxy uses.

## HTTP shim — usage

```python
from client import WandProxy
resp = WandProxy().call({"method": "GET", "path": "/ping"})  # via the proxy
```
