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

func (s stubCompleter) Complete(_ context.Context, _, user string) (string, error) {
	for src, verdict := range s.verdicts {
		if strings.Contains(user, "Source file: "+src+"\n") {
			return verdict, nil
		}
	}
	return "QUALIFIES", nil
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

func (e *errCompleter) Complete(ctx context.Context, _, user string) (string, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	if strings.Contains(user, "Source file: "+e.failOn+"\n") {
		return "", e.err
	}
	// Respect cancellation so calls launched after the failure abort rather
	// than returning a verdict.
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "QUALIFIES", nil
}

func TestScanSourcesBailsOnError(t *testing.T) {
	d := t.TempDir()
	for _, rel := range []string{"a.py", "b.py", "c.py"} {
		if err := os.WriteFile(filepath.Join(d, rel), []byte("x = 1\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	t.Chdir(d)

	wantErr := errors.New("claude request failed: boom")
	client := &errCompleter{failOn: "b.py", err: wantErr}

	_, err := scanSources(client, []string{"a.py", "b.py", "c.py"}, nil)
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
	mk("svc.py", "import requests\n") // qualifies, no test yet
	mk("data.py", "x = 1\n")          // does not qualify
	mk("cached.py", "import httpx\n") // qualifies, but a test already exists
	mk("notes.md", "# docs\n")        // qualifies per stub, but no test-path convention
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

	results, err := scanSources(client, order, progress)
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
