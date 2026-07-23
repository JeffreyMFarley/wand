---
name: wand-doctor
description: >
  Classify how stored wand fixtures have drifted from live service responses.
  Runs the livetest divergences the proxy recorded and labels each one
  BREAKING, BENIGN, or NOISE with a one-line reason, exiting non-zero if any
  are breaking. Use when asked to health-check fixtures, diagnose fixture
  drift, check whether an upstream API changed, or run "wand doctor". Requires
  that tests were first run with WAND_MODE=livetest to record divergences.
---

# Doctor: classify fixture drift (wand)

The proxy records divergences when tests run under `WAND_MODE=livetest`
(stored fixture vs. live response). This skill classifies each one.

## 1. Load recorded divergences

Read the divergences the proxy stored (under the fixtures path, default
`__fixtures__/`). If there are none, tell the user to run their tests with
`WAND_MODE=livetest` first, then re-run doctor — and stop.

## 2. Classify each divergence

For each divergence, compare the stored fixture response against the live
response and assign exactly one class:

- **BREAKING** — schema change, removed fields, or changed semantics.
- **BENIGN** — additive optional fields only.
- **NOISE** — timestamps, durations, or cursor values that really belong in
  the normalization config (`services/*.yaml`), not in the fixture.

Print one line per divergence:

```
[BREAKING] <service> <hash> — <one-line reason>
```

## 3. Summarize and signal

End with a count: `N divergence(s) classified; M breaking.` If any are
breaking, say so clearly (this is the non-zero-exit condition). For NOISE
items, suggest the normalization rule that would absorb them.

---

**Plumbing this skill relies on:** the proxy's `livetest` mode to produce
divergences; the recorded divergence files under the fixtures path.
