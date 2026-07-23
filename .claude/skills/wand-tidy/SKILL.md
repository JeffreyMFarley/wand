---
name: wand-tidy
description: >
  Reclaim stale wand fixtures — the orphans left behind when a code change alters
  a request shape, so capture writes a new fixture and the old one lingers
  forever. Drives the reachability sweep: clear the access log, run the full test
  suite in ci mode to mark which fixtures are still used, review the unreached
  ones, then delete them. Runs entirely in ci mode, so no live credentials are
  needed. Use when asked to tidy fixtures, prune/clean up/garbage-collect stale
  or orphaned fixtures, remove fixtures no longer used after a refactor, or run
  "wand tidy".
---

# Tidy: reclaim orphaned fixtures (wand)

wand fixtures are content-addressed, so capture is **purely additive**: when a
code change alters a request, it gets a new hash and a new fixture, and the old
fixture is orphaned but never removed. `wand tidy` is the reachability sweep that
reclaims them — the `git gc` to capture's additive `git fetch`, not a
delete-everything-and-recapture ritual. It replays in `ci` mode, so it needs **no
live credentials**.

The sweep is only trustworthy if the marking run exercised the **whole** suite
and it **passed**. The tool cannot see the test runner's exit code, so confirming
a full, green run is the human's job. Walk the user through these steps; do not
run `--force` on the user's behalf without a confirmed full, passing run.

## 1. Reset the access log

```
wand tidy --reset
```

Clears `__fixtures__/access.jsonl` so reachability is marked from a clean slate.
This matters after a code change: stale marks from a previous run would keep
now-dead fixtures looking alive, and the orphans would never be reclaimed.

## 2. Run the FULL test suite in ci mode

The user runs their entire suite so every live fixture gets marked as reached:

```
WAND_MODE=ci <their test command>     # e.g. make test, pytest, go test ./...
```

Confirm two things before continuing: the run covered the **whole** suite (not a
subset), and it **passed**. A partial run makes fixtures for the un-run tests look
unreached — deleting those would be data loss.

## 3. Dry-run the sweep and review

```
wand tidy
```

This lists the fixtures the run never touched (`service  hash  scenario`) and
changes nothing. Review the list with the user — each line is a deletion
candidate. If it reports a heads-up about fixtures "requested but absent", those
are calls the code makes with no stored fixture; they do **not** block tidy, but
flag them as a possible coverage gap worth capturing.

## 4. Delete, once the list looks right

```
wand tidy --force
```

Removes the orphan fixture pairs, drops their `index.json` entries, and clears the
access log. Deletions are git-tracked, so a mistaken sweep is recoverable with
`git checkout -- __fixtures__`. As a final check, have the user re-run the suite:
it should still pass, proving the deleted fixtures were truly dead.

---

**Plumbing this skill relies on:** the access log both the proxy and the boto3
shim append during a `ci` run (`__fixtures__/access.jsonl`); the content-addressed
fixture store and `index.json` under the fixtures path. No Claude calls and no API
key — `tidy` is a plumbing command.
