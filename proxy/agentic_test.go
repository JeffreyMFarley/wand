package proxy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// stubCompleter stands in for ClaudeClient in scan/scaffold tests: it returns a
// canned verdict per source path (matched against the "Source file: <path>"
// header in the user message) so no real API call is made. Unlisted files
// default to qualifying.
type stubCompleter struct {
	verdicts map[string]string // src path -> Complete() response
}

func (s stubCompleter) CompleteWithUsage(_ context.Context, _, user string) (string, Usage, error) {
	for src, verdict := range s.verdicts {
		if strings.Contains(user, "Source file: "+src+"\n") {
			return verdict, Usage{}, nil
		}
	}
	return "QUALIFIES", Usage{}, nil
}

// errCompleter fails on the request whose user message names failOn, and
// counts how many calls it received, so a test can assert the sweep bailed
// instead of classifying every file.
type errCompleter struct {
	failOn string
	err    error
	mu     sync.Mutex
	calls  int
}

func (e *errCompleter) CompleteWithUsage(ctx context.Context, _, user string) (string, Usage, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	if strings.Contains(user, "Source file: "+e.failOn+"\n") {
		return "", Usage{}, e.err
	}
	// Respect cancellation so calls launched after the failure abort rather
	// than returning a verdict.
	if err := ctx.Err(); err != nil {
		return "", Usage{}, err
	}
	return "QUALIFIES", Usage{}, nil
}

// newTestCache returns an empty verdict cache backed by a throwaway file, so a
// scan test exercises the real cache path without touching the working tree.
func newTestCache(t *testing.T) *scanCache {
	t.Helper()
	c := openScanCache(filepath.Join(t.TempDir(), "scan-cache.jsonl"), "test-v1")
	t.Cleanup(func() { c.Close() })
	return c
}

func TestScanSourcesBailsOnError(t *testing.T) {
	d := t.TempDir()
	// Each file carries a signal token (so the pre-filter passes it through to
	// the failing LLM call) AND distinct content: identical content would share
	// one content-addressed cache entry, so a's verdict would spare b the call.
	for _, rel := range []string{"a.py", "b.py", "c.py"} {
		body := "import requests\n# " + rel + "\n"
		if err := os.WriteFile(filepath.Join(d, rel), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	t.Chdir(d)

	wantErr := errors.New("claude request failed: boom")
	client := &errCompleter{failOn: "b.py", err: wantErr}

	_, err := scanSources(client, newTestCache(t), []string{"a.py", "b.py", "c.py"}, nil)
	if err == nil {
		t.Fatal("scanSources returned nil error, want the failing call's error")
	}
	// The real failure must surface, not a cancellation-fallout error masking it.
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("scanSources error = %v, want it to carry %q", err, wantErr)
	}
}

func TestTestPathFor(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{filepath.Join("pkg", "report.go"), filepath.Join("pkg", "report_test.go")},
		{filepath.Join("pkg", "report.py"), filepath.Join("tests", "pkg", "test_report.py")},
		{"report.py", filepath.Join("tests", "test_report.py")},
		{filepath.Join("src", "report.ts"), filepath.Join("src", "report.test.ts")},
		{filepath.Join("src", "report.jsx"), filepath.Join("src", "report.test.jsx")},
		{filepath.Join("app", "Report.php"), filepath.Join("app", "ReportTest.php")},
		{filepath.Join("lib", "report.rb"), filepath.Join("spec", "lib", "report_spec.rb")},
		{filepath.Join("src", "main", "java", "com", "acme", "Report.java"), filepath.Join("src", "test", "java", "com", "acme", "ReportTest.java")},
		{filepath.Join("com", "acme", "Report.java"), filepath.Join("com", "acme", "ReportTest.java")},
	}
	for _, c := range cases {
		got, err := testPathFor(c.src)
		if err != nil {
			t.Fatalf("testPathFor(%q) error: %v", c.src, err)
		}
		if got != c.want {
			t.Errorf("testPathFor(%q) = %q, want %q", c.src, got, c.want)
		}
	}
	if _, err := testPathFor("README.md"); err == nil {
		t.Error("expected error for unsupported extension, got nil")
	}
}

func TestIsSourceFile(t *testing.T) {
	source := []string{"report.py", "report.go", "report.ts", "app.jsx", "Report.php", "report.rb", "Report.java"}
	notSource := []string{"test_report.py", "report_test.go", "report.test.ts", "report.spec.tsx", "ReportTest.php", "report_spec.rb", "ReportTest.java", "README.md", "data.json"}
	for _, n := range source {
		if !isSourceFile(n) {
			t.Errorf("isSourceFile(%q) = false, want true", n)
		}
	}
	for _, n := range notSource {
		if isSourceFile(n) {
			t.Errorf("isSourceFile(%q) = true, want false", n)
		}
	}
}

func TestNoIntegrationTest(t *testing.T) {
	skip := map[string]string{
		"No integration test. Pure data container":     "Pure data container",
		"> No integration test. Redux middleware only": "Redux middleware only",
		"No integration test":                          "no qualifying external calls",
	}
	for in, wantReason := range skip {
		reason, isSkip := noIntegrationTest(in)
		if !isSkip {
			t.Errorf("noIntegrationTest(%q) skip = false, want true", in)
		}
		if reason != wantReason {
			t.Errorf("noIntegrationTest(%q) reason = %q, want %q", in, reason, wantReason)
		}
	}
	if _, isSkip := noIntegrationTest("import unittest\nclass T(...)"); isSkip {
		t.Error("noIntegrationTest on real test code returned skip = true")
	}
}

func TestQualifiesReason(t *testing.T) {
	cases := map[string]string{
		"QUALIFIES. Calls requests.get() over HTTP": "Calls requests.get() over HTTP",
		"> QUALIFIES. Runs a Snowflake query":       "Runs a Snowflake query",
		"QUALIFIES":                                 "", // bare verdict, no reason given
		"No integration test. Pure data container":  "", // not a qualification
	}
	for in, want := range cases {
		if got := qualifiesReason(in); got != want {
			t.Errorf("qualifiesReason(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseTestListJSON(t *testing.T) {
	got := parseTestList("Here you go:\n[\"a::t1\", \"b::t2\"]")
	if len(got) != 2 || got[0] != "a::t1" || got[1] != "b::t2" {
		t.Fatalf("got %#v", got)
	}
}

func TestParseTestListFallbackToLines(t *testing.T) {
	got := parseTestList("- tests/test_a.py::test_one\n- tests/test_b.py::test_two")
	if len(got) != 2 || got[0] != "tests/test_a.py::test_one" {
		t.Fatalf("got %#v", got)
	}
}

func TestSplitClass(t *testing.T) {
	cases := map[string]string{
		"BREAKING\nremoved the id field": "BREAKING",
		"benign — added optional field":  "BENIGN",
		"NOISE: timestamp differs":       "NOISE",
		"I am not sure what this is":     "UNKNOWN",
	}
	for in, want := range cases {
		if got, _ := splitClass(in); got != want {
			t.Fatalf("splitClass(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFirstLineTrimsQuotes(t *testing.T) {
	if got := firstLine("\"books search by author\"\nextra"); got != "books search by author" {
		t.Fatalf("got %q", got)
	}
}

func TestStripFences(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"plain fenced block": {
			in:   "```python\nprint(1)\n```",
			want: "print(1)",
		},
		"preamble before the fence is discarded": {
			in:   "I'll analyze the file.\n\n**Step 6**\n\n```python\nprint(1)\n```",
			want: "print(1)",
		},
		"trailing prose after the fence is discarded": {
			in:   "```python\nprint(1)\n```\nThat's the file.",
			want: "print(1)",
		},
		"no fence returns raw body": {
			in:   "print(1)",
			want: "print(1)",
		},
	}
	for name, c := range cases {
		if got := stripFences(c.in); got != c.want {
			t.Fatalf("%s: stripFences(%q) = %q, want %q", name, c.in, got, c.want)
		}
	}
}

func TestScanSources(t *testing.T) {
	d := t.TempDir()
	mk := func(rel, content string) {
		p := filepath.Join(d, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	// Source files under scan. Content is irrelevant — the stub keys off path.
	mk("svc.py", "import requests\n")         // qualifies, no test yet
	mk("data.py", "import requests\nx = 1\n") // reaches the LLM (signal token), stub says no
	mk("cached.py", "import httpx\n")         // qualifies, but a test already exists
	mk("notes.md", "# docs\n")                // qualifies per stub, but no test-path convention
	// Pre-existing test for cached.py: testPathFor("cached.py") -> tests/test_cached.py
	mk(filepath.Join("tests", "test_cached.py"), "# existing\n")

	t.Chdir(d)

	client := stubCompleter{verdicts: map[string]string{
		"data.py": "No integration test. Pure data container",
	}}

	order := []string{"svc.py", "data.py", "cached.py", "notes.md"}

	// Track progress callbacks: it must fire once per file with a monotonically
	// increasing done count. Guarded because scanSources classifies concurrently.
	var mu sync.Mutex
	var counts []int
	progress := func(done, total int, _ scanClassification) {
		mu.Lock()
		defer mu.Unlock()
		if total != len(order) {
			t.Errorf("progress total = %d, want %d", total, len(order))
		}
		counts = append(counts, done)
	}

	results, err := scanSources(client, newTestCache(t), order, progress)
	if err != nil {
		t.Fatalf("scanSources error: %v", err)
	}

	// One callback per file, counting 1..N regardless of completion order.
	if len(counts) != len(order) {
		t.Errorf("progress fired %d times, want %d", len(counts), len(order))
	}
	sort.Ints(counts)
	for i, c := range counts {
		if c != i+1 {
			t.Errorf("progress counts = %v, want 1..%d", counts, len(order))
			break
		}
	}

	// Results preserve input order even though classification is concurrent.
	for i, r := range results {
		if r.src != order[i] {
			t.Errorf("results[%d].src = %q, want %q (input order not preserved)", i, r.src, order[i])
		}
	}

	bySrc := map[string]scanClassification{}
	for _, r := range results {
		bySrc[r.src] = r
	}
	if len(bySrc) != 4 {
		t.Fatalf("expected 4 classifications, got %d: %+v", len(bySrc), results)
	}

	// svc.py: qualifies, no existing test.
	if got := bySrc["svc.py"]; !got.qualifies || got.exists ||
		got.dest != filepath.Join("tests", "test_svc.py") {
		t.Errorf("svc.py = %+v, want qualifies, !exists, dest tests/test_svc.py", got)
	}
	// data.py: does not qualify, reason carried through from the prompt.
	if got := bySrc["data.py"]; got.qualifies || got.reason != "Pure data container" {
		t.Errorf("data.py = %+v, want !qualifies with reason 'Pure data container'", got)
	}
	// cached.py: qualifies but the test already exists.
	if got := bySrc["cached.py"]; !got.qualifies || !got.exists {
		t.Errorf("cached.py = %+v, want qualifies and exists", got)
	}
	// notes.md: qualifies per stub, but no test-path convention -> skipped.
	if got := bySrc["notes.md"]; got.qualifies {
		t.Errorf("notes.md = %+v, want !qualifies (unsupported extension)", got)
	} else if !strings.Contains(got.reason, "unsupported") {
		t.Errorf("notes.md reason = %q, want it to mention 'unsupported'", got.reason)
	}
}

// countingCompleter always qualifies and counts how many LLM calls it received,
// so a test can assert the cache and pre-filter kept work away from the model.
type countingCompleter struct {
	mu    sync.Mutex
	calls int
}

func (c *countingCompleter) CompleteWithUsage(_ context.Context, _, _ string) (string, Usage, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	// A known, non-zero usage so a test can assert the scan meters only the
	// tokens from real LLM calls (7+3 = 10 per call).
	return "QUALIFIES", Usage{InputTokens: 7, OutputTokens: 3}, nil
}

func TestMayMakeExternalCall(t *testing.T) {
	long := strings.Repeat("total = total + 1\n", godFunctionLines+10) // no signal token
	cases := []struct {
		name string
		src  string
		data string
		want bool
	}{
		{"signal token present", "svc.py", "import requests\n", true},
		{"no signal, short file", "data.py", "x = 1\ny = 2\n", false},
		{"no signal but god-function length", "big.py", long, true},
		{"unknown extension defers to model", "notes.md", "nothing here\n", true},
		{"signal match is case-insensitive", "svc.go", "import \"NET/HTTP\"\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mayMakeExternalCall(c.src, []byte(c.data)); got != c.want {
				t.Errorf("mayMakeExternalCall(%q) = %v, want %v", c.src, got, c.want)
			}
		})
	}
}

func TestScaffoldUsesCacheAndPrefilter(t *testing.T) {
	d := t.TempDir()
	mk := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(d, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mk("pure.py", "x = 1\n")             // no signal, short -> pre-filtered
	mk("client.py", "import requests\n") // qualifies
	t.Chdir(d)

	cache := newTestCache(t)
	client := &countingCompleter{}
	r := NewRouter(Config{})

	// Pre-filter: scaffold skips a file that provably makes no external call
	// without any API call at all.
	wrote, err := r.scaffoldOne(client, cache, "pure.py")
	if err != nil {
		t.Fatalf("scaffoldOne pure.py: %v", err)
	}
	if wrote {
		t.Error("scaffoldOne wrote a test for a pre-filtered file, want skip")
	}
	if client.calls != 0 {
		t.Errorf("pre-filtered file cost %d calls, want 0", client.calls)
	}

	// Prime the shared cache the way `wand scan` would: one qualify call.
	res, err := classifyForScan(context.Background(), client, cache, "client.py")
	if err != nil {
		t.Fatalf("classifyForScan client.py: %v", err)
	}
	if !res.qualifies || res.route != routeAsked {
		t.Fatalf("scan verdict = %+v, want asked + qualifying", res)
	}
	if client.calls != 1 {
		t.Fatalf("scan made %d calls, want 1 (qualify)", client.calls)
	}

	// Scaffold on the same file must reuse the cached verdict, not re-qualify:
	// the only new call is the write step, so the counter advances by exactly 1.
	wrote, err = r.scaffoldOne(client, cache, "client.py")
	if err != nil {
		t.Fatalf("scaffoldOne client.py: %v", err)
	}
	if !wrote {
		t.Error("scaffoldOne wrote nothing for a qualifying file, want a generated test")
	}
	if client.calls != 2 {
		t.Errorf("total calls = %d, want 2 (scan qualify + scaffold write; qualify reused from cache)", client.calls)
	}
	if _, err := os.Stat(filepath.Join("tests", "test_client.py")); err != nil {
		t.Errorf("expected generated test at tests/test_client.py: %v", err)
	}
}

func TestQualifyingReasonIsCachedAndReported(t *testing.T) {
	d := t.TempDir()
	body := []byte("import requests\n")
	if err := os.WriteFile(filepath.Join(d, "svc.py"), body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Chdir(d)

	cache := newTestCache(t)
	client := stubCompleter{verdicts: map[string]string{
		"svc.py": "QUALIFIES. Calls requests.get() over HTTP",
	}}

	res, err := classifyForScan(context.Background(), client, cache, "svc.py")
	if err != nil {
		t.Fatalf("classifyForScan: %v", err)
	}
	if !res.qualifies || res.reason != "Calls requests.get() over HTTP" {
		t.Errorf("res = %+v, want qualifying with the reason carried through", res)
	}
	// The reason must be persisted in the cache, not just the qualifies boolean,
	// so a later scan/scaffold run reports why the file qualifies without a call.
	if v, ok := cache.get(Hash(body)); !ok || !v.Qualifies || v.Reason != "Calls requests.get() over HTTP" {
		t.Errorf("cached verdict = %+v (ok=%v), want qualifying with reason", v, ok)
	}
}

func TestHumanTokens(t *testing.T) {
	cases := map[int]string{
		0:         "0",
		42:        "42",
		999:       "999",
		1_000:     "1.0K",
		1_234:     "1.2K",
		12_345:    "12.3K",
		1_000_000: "1.0M",
		2_500_000: "2.5M",
	}
	for n, want := range cases {
		if got := humanTokens(n); got != want {
			t.Errorf("humanTokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestScanCacheRoundTripAndVersioning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scan-cache.jsonl")

	c := openScanCache(path, "v1")
	c.put("hash-a", true, "")
	c.put("hash-b", false, "pure data")
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopening at the same version replays both verdicts.
	reopened := openScanCache(path, "v1")
	defer reopened.Close()
	if v, ok := reopened.get("hash-a"); !ok || !v.Qualifies {
		t.Errorf("hash-a = %+v (ok=%v), want a qualifying hit", v, ok)
	}
	if v, ok := reopened.get("hash-b"); !ok || v.Qualifies || v.Reason != "pure data" {
		t.Errorf("hash-b = %+v (ok=%v), want non-qualifying hit with reason", v, ok)
	}

	// A different version (prompt or model changed) ignores every stored line.
	stale := openScanCache(path, "v2")
	defer stale.Close()
	if _, ok := stale.get("hash-a"); ok {
		t.Error("hash-a hit under version v2, want miss (stale verdict must be ignored)")
	}
}

func TestClassifyForScanSkipsModelForCacheAndPrefilter(t *testing.T) {
	d := t.TempDir()
	mk := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(d, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mk("pure.py", "x = 1\n")          // no signal, short -> pre-filtered, no LLM
	mk("svc.py", "import requests\n") // signal -> first run asks, second run cached
	t.Chdir(d)

	cache := newTestCache(t)
	client := &countingCompleter{}
	ctx := context.Background()

	// Pre-filtered: rejected without an API call.
	got, err := classifyForScan(ctx, client, cache, "pure.py")
	if err != nil {
		t.Fatalf("classify pure.py: %v", err)
	}
	if got.route != routePrefiltered || got.qualifies || got.reason != prefilterReason {
		t.Errorf("pure.py = %+v, want pre-filtered non-qualifying with reason %q", got, prefilterReason)
	}
	if got.tokens != 0 {
		t.Errorf("pure.py tokens = %d, want 0 (pre-filtered, no API call)", got.tokens)
	}

	// First real classification hits the model once and meters its tokens.
	got, err = classifyForScan(ctx, client, cache, "svc.py")
	if err != nil {
		t.Fatalf("classify svc.py: %v", err)
	}
	if got.route != routeAsked || !got.qualifies {
		t.Errorf("svc.py (first) = %+v, want asked and qualifying", got)
	}
	if got.tokens != 10 {
		t.Errorf("svc.py (first) tokens = %d, want 10 (7 in + 3 out)", got.tokens)
	}

	// Second classification of the same content is served from the cache — no
	// call, so no tokens are charged this run.
	got, err = classifyForScan(ctx, client, cache, "svc.py")
	if err != nil {
		t.Fatalf("re-classify svc.py: %v", err)
	}
	if got.route != routeCached || !got.qualifies {
		t.Errorf("svc.py (second) = %+v, want cached and qualifying", got)
	}
	if got.tokens != 0 {
		t.Errorf("svc.py (second) tokens = %d, want 0 (cache hit, no API call)", got.tokens)
	}

	if client.calls != 1 {
		t.Errorf("model was called %d times, want exactly 1 (pre-filter + cache spared the rest)", client.calls)
	}
}
