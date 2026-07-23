---
name: wand-scaffold-tests
description: >
  Generate integration tests for source files that make direct external calls
  (Snowflake, HTTP APIs, gRPC, AWS SDK, database/Redis/Elasticsearch clients).
  Use when asked to scaffold, generate, write, or add integration tests for
  one or more source files or directories. Supports Python, Go, Node/JS/TS,
  Laravel/PHP, Ruby, and Java. Do NOT use for unit tests of pure logic. If the
  user only wants to know which files WOULD get tests (scope, not generation),
  use wand-scan instead.
---

# Scaffold integration tests (wand)

This is the `wand scaffold` workflow: **qualify** which files deserve a test,
then **write** the tests. Qualification is owned by the `wand-scan` skill; this
skill reuses it, then generates.

Tests you produce are meant to run against the **wand proxy**, not the live
service — so **never mock external calls**. Mocking is handled at the network
layer by the proxy.

## 1. Qualify (via the wand-scan skill)

Run the **`wand-scan`** skill on the given paths to resolve targets and get a
`QUALIFIES` / `No integration test` verdict per file. That skill owns the
target-resolution rules and the qualification checklist
([../wand-scan/references/qualify.md](../wand-scan/references/qualify.md)) —
do not restate them here. Carry forward only the files that qualify.

## 2. Write tests for qualifying files

**Read [references/write.md](references/write.md)** and, for each qualifying
file, produce exactly one test file following that checklist precisely. Write
it next to the source using the language's naming convention
(`test_x.py`, `x_test.go`, `x.test.js`, `xTest.php`, `x_spec.rb`, `XTest.java`).

If, on the closer reading that `write.md` requires, a file turns out to have no
subject that reaches an external service directly, record it as
`No integration test. <reason>` instead of writing a file.

## 3. Report

Summarize: files scanned, files that qualified, test files written (with
paths), and files skipped with their reasons.

---

**Plumbing this skill relies on:** none at generation time. To exercise the
generated tests, the developer runs them through the proxy
(`wand proxy start`, then `WAND_MODE=ci <test command>`).
