---
name: wand-capture
description: >
  Drive the wand fixture-capture ritual: figure out which tests to capture
  fixtures for (from explicit paths or by inferring scope from the git diff),
  print the WAND_MODE=capture command to record them against live services,
  then name each captured scenario and update index.json. Use when asked to
  capture fixtures, record API responses, refresh fixtures for changed code,
  or name/label captured fixtures. Requires live credentials only during the
  recording step, never in CI.
---

# Capture fixtures (wand)

Three entry points — pick by what the user asked:

- **Explicit scope** — they named test files/dirs → go to §1.
- **Infer from changes** — "capture whatever my diff touches" → §2.
- **Post-capture naming** — "name the fixtures I just captured" → §3.

Capturing hits **live services and writes fixture files**, so it always shows
the proposed scope and waits for confirmation before recording — scope creep
produces large, unreviewable fixture commits.

## 1. Explicit scope

Resolve the given paths to **test files** (extensions/patterns: `*_test.go`,
`test_*.py`/`*_test.py`, `*.test.*`/`*.spec.*`, `*Test.php`, `*_spec.rb`,
`*Test.java`; skip `.git`, `node_modules`, `vendor`, `__fixtures__`, `dist`,
`build`). Error on any missing path. Then print the capture command (§4).

## 2. Infer scope from the git diff

1. Get the working-tree diff: `git diff HEAD`. If empty, say so and stop.
2. Read `index.json` (under the fixtures path, default `__fixtures__/`) to see
   which tests each existing fixture already backs.
3. From the diff + optional user description, identify the **small, precise**
   set of existing tests that exercise the changed code paths. Prefer fewer.
4. Present the proposed test list and **ask the user to confirm** before
   proceeding. On confirmation, print the capture command (§4).

## 3. Name captured scenarios (post-capture)

For every fixture whose `index.json` entry has an empty `scenario`:

1. Read the fixture's request + response from the store (under the fixtures
   path, grouped by service).
2. Give it a **short scenario name (max 10 words)** describing what it covers.
3. Fill in the entry: `scenario`, `service`, and if absent, `captured` (today,
   `YYYY-MM-DD`), `captured_by` (`wand/1.0.0`), and a `request_summary`
   (first ~200 chars of the request).
4. Write `index.json` back. Report each `hash → name` you set, or say all
   fixtures were already named.

## 4. The capture command to print

Tell the developer to run their tests in capture mode with live credentials in
their environment, e.g.:

```bash
WAND_MODE=capture <their test command> <the resolved test targets>
```

Then remind them to run the naming step (§3) and commit `__fixtures__/`.

---

**Plumbing this skill relies on:** `git diff HEAD` (scope inference);
`index.json` and the fixture store files (read for naming, write for the
index). Recording itself is done by the proxy under `WAND_MODE=capture`.
