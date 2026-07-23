---
name: wand-scan
description: >
  Report which source files would get integration tests — the scope of the
  problem, without generating anything. Scans files/dirs and gives each a
  QUALIFIES / "No integration test" verdict based on whether it makes direct
  external calls (Snowflake, HTTP, gRPC, AWS SDK, DB/Redis/Elasticsearch
  clients). Supports Python, Go, Node/JS/TS, Laravel/PHP, Ruby, Java. Use when
  asked to scan, to see which files need tests, to scope test coverage, or to
  run "wand scan". This skill NEVER writes tests — to generate them, use
  wand-scaffold-tests.
---

# Scan: scope which files need integration tests (wand)

This is `wand scan` — a **dry run of the qualification phase only**. It tells
you the shape of the problem and writes nothing. Generation is a separate
skill (`wand-scaffold-tests`).

## 1. Resolve targets

Given files and/or directories:

- An explicit **file** argument is taken as-is.
- A **directory** is walked recursively. Keep source files only; skip test
  files and the dirs `.git`, `node_modules`, `vendor`, `__fixtures__`,
  `dist`, `build`.
- Source extensions: `.go .py .js .jsx .ts .tsx .php .rb .java`.
- Test files to exclude: `*_test.go`, `test_*.py` / `*_test.py`, `*.test.*` /
  `*.spec.*` (js/ts), `*Test.php`, `*_spec.rb`, `*Test.java`.
- If any named path does not exist, stop and report it — never silently
  resolve a typo to nothing.

## 2. Qualify each file

**Read [references/qualify.md](references/qualify.md)** and apply it to every
resolved file. Each file gets exactly one verdict:

- `QUALIFIES`, or
- `No integration test. <one-sentence reason>`

## 3. Report scope — and stop

Print a table: file → verdict (with the one-line reason for non-qualifiers).
End with a count: `N files scanned, M qualify for integration tests.`

**Generate nothing.** If the user then wants the tests written, hand off to
the `wand-scaffold-tests` skill.

---

**Plumbing this skill relies on:** none — it only reads source files.
