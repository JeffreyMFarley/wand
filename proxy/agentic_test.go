package proxy

import "testing"

func TestParseScaffoldExtractsPathAndStripsFences(t *testing.T) {
	out := "path: tests/test_new.py\n\n```python\ndef test_x():\n    assert True\n```"
	path, code := parseScaffold(out)
	if path != "tests/test_new.py" {
		t.Fatalf("path = %q", path)
	}
	if code != "def test_x():\n    assert True" {
		t.Fatalf("code = %q", code)
	}
}

func TestParseScaffoldNoPath(t *testing.T) {
	path, code := parseScaffold("def test_x():\n    pass")
	if path != "" {
		t.Fatalf("expected empty path, got %q", path)
	}
	if code != "def test_x():\n    pass" {
		t.Fatalf("code = %q", code)
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

func TestSplitList(t *testing.T) {
	got := splitList("a, b ,,c")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("got %#v", got)
	}
}
