package proxy

import (
	"path/filepath"
	"testing"
)

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
	source := []string{"report.py", "report.go", "report.ts", "app.jsx"}
	notSource := []string{"test_report.py", "report_test.go", "report.test.ts", "report.spec.tsx", "README.md", "data.json"}
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
