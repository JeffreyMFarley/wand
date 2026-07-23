---
name: wand-diff-fixtures
description: >
  Summarize what changed in wand fixture files in plain English for a
  pull-request description. Focuses on behavioral and schema changes
  (new/removed fields, changed values, new scenarios) and ignores pure
  formatting. Use when asked to describe fixture changes, write a PR summary
  of fixture updates, or diff fixtures — either the working tree or a specific
  PR number.
---

# Diff fixtures for a PR summary (wand)

## 1. Get the fixture diff

- Default (working tree): `git diff HEAD -- <fixtures path>` (fixtures path
  comes from `wand.yaml` `fixtures.path`, default `__fixtures__`).
- If the user gives a PR number: `gh pr diff <number>` (requires the `gh` CLI
  authenticated; if it fails, say so).

If the diff is empty, report "no fixture changes detected" and stop.

## 2. Summarize behaviorally

Write a few concise bullet points covering:

- New or removed fields
- Changed values
- New or removed scenarios

**Ignore pure formatting/whitespace churn.** The output is meant to drop into
a PR description, so keep it tight and reviewer-oriented.

---

**Plumbing this skill relies on:** `git diff HEAD -- <fixtures path>` or
`gh pr diff <number>`; `wand.yaml` for the fixtures path.
