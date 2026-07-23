---
name: wand-explain-fixture
description: >
  Explain what scenario a single stored wand fixture covers, given its hash
  (or a unique hash prefix). Describes the scenario and its key parameters in
  a few sentences. Use when asked what a fixture is for, what scenario a hash
  covers, or to explain/describe a specific fixture.
---

# Explain a fixture (wand)

## 1. Resolve the fixture

Given a hash (or a unique prefix), locate the fixture in the store (under the
fixtures path, default `__fixtures__/`, grouped by service). If nothing
matches, say so and stop. If a prefix is ambiguous, list the matches.

## 2. Gather context

Read the fixture's request and response bodies, and its `index.json` entry
(service, and any tests that use it).

## 3. Describe it

In **2–4 sentences**, describe what scenario this fixture covers and the key
parameters (e.g. the query shape, the search term, the resource id, notable
request options). Be concise and concrete — no preamble. Lead with the
fixture's hash and service.

---

**Plumbing this skill relies on:** the fixture store files and `index.json`
under the fixtures path.
