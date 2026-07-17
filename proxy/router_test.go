package proxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRouterRunSupportsScaffoldedCommands(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		checkFile string
	}{
		// Only offline, deterministic commands belong here. The Claude-powered
		// commands (capture <file-or-dir>, diff --pr, explain, scaffold) do real
		// work needing credentials or fixtures and are covered by helper unit
		// tests plus manual end-to-end runs instead.
		{name: "init", args: []string{"init"}, checkFile: "wand.yaml"},
		{name: "help", args: []string{"help"}},
		{name: "proxy start", args: []string{"proxy", "start"}},
		{name: "proxy stop", args: []string{"proxy", "stop"}},
		{name: "diff", args: []string{"diff"}},
		{name: "doctor", args: []string{"doctor"}},
		{name: "verify", args: []string{"verify"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := t.TempDir()
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatalf("getwd: %v", err)
			}
			defer os.Chdir(cwd)

			if err := os.Chdir(d); err != nil {
				t.Fatalf("chdir: %v", err)
			}
			if tt.args[0] == "proxy" {
				if err := os.Setenv("WAND_NONBLOCKING", "1"); err != nil {
					t.Fatalf("Setenv: %v", err)
				}
			}

			r := NewRouter(Config{})
			if err := r.Run(tt.args); err != nil {
				t.Fatalf("Run(%v) returned error: %v", tt.args, err)
			}

			if tt.checkFile != "" {
				if _, err := os.Stat(filepath.Join(d, tt.checkFile)); err != nil {
					t.Fatalf("expected %s to be created: %v", tt.checkFile, err)
				}
			}
		})
	}
}

func TestRunInitInstallsPythonShims(t *testing.T) {
	shimDir := filepath.Join(".wand", "shims", "python")

	tests := []struct {
		name    string
		files   map[string]string // project files to create before init
		present []string          // shim modules expected to be installed
		absent  []string          // shim modules expected NOT to be installed
	}{
		{
			name:    "not a python project",
			files:   map[string]string{"go.mod": "module x\n"},
			present: nil,
			absent:  []string{"client.py", "marks.py", "boto3_shim.py"},
		},
		{
			name:    "python project without boto3",
			files:   map[string]string{"requirements.txt": "requests==2.31.0\npytest\n"},
			present: []string{"client.py", "marks.py"},
			absent:  []string{"boto3_shim.py"},
		},
		{
			name:    "python project with boto3",
			files:   map[string]string{"requirements.txt": "requests\nboto3>=1.34\n"},
			present: []string{"client.py", "marks.py", "boto3_shim.py"},
			absent:  nil,
		},
		{
			name:    "detected via loose .py file",
			files:   map[string]string{"app.py": "print('hi')\n"},
			present: []string{"client.py", "marks.py"},
			absent:  []string{"boto3_shim.py"},
		},
		{
			name:    "boto3 declared in pyproject",
			files:   map[string]string{"pyproject.toml": "[project]\ndependencies = [\"botocore\"]\n"},
			present: []string{"boto3_shim.py"},
			absent:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := t.TempDir()
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatalf("getwd: %v", err)
			}
			defer os.Chdir(cwd)
			if err := os.Chdir(d); err != nil {
				t.Fatalf("chdir: %v", err)
			}
			for name, content := range tt.files {
				if err := os.WriteFile(filepath.Join(d, name), []byte(content), 0o644); err != nil {
					t.Fatalf("write %s: %v", name, err)
				}
			}

			r := NewRouter(Config{})
			if err := r.Run([]string{"init"}); err != nil {
				t.Fatalf("init returned error: %v", err)
			}

			for _, mod := range tt.present {
				if _, err := os.Stat(filepath.Join(d, shimDir, mod)); err != nil {
					t.Errorf("expected shim %s to be installed: %v", mod, err)
				}
			}
			for _, mod := range tt.absent {
				if _, err := os.Stat(filepath.Join(d, shimDir, mod)); !os.IsNotExist(err) {
					t.Errorf("expected shim %s to be absent, stat err = %v", mod, err)
				}
			}

			// A root conftest.py that activates the boto3 bridge is created
			// only for boto3 projects.
			wantsBoto3 := contains(tt.present, "boto3_shim.py")
			_, statErr := os.Stat(filepath.Join(d, "conftest.py"))
			if wantsBoto3 && statErr != nil {
				t.Errorf("expected root conftest.py for a boto3 project: %v", statErr)
			}
			if !wantsBoto3 && !os.IsNotExist(statErr) {
				t.Errorf("expected no root conftest.py for a non-boto3 project, stat err = %v", statErr)
			}
		})
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func TestRunInitDoesNotClobberExistingConftest(t *testing.T) {
	d := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer os.Chdir(cwd)
	if err := os.Chdir(d); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(d, "requirements.txt"), []byte("boto3\n"), 0o644); err != nil {
		t.Fatalf("write requirements: %v", err)
	}
	const existing = "# my own fixtures\n"
	if err := os.WriteFile(filepath.Join(d, "conftest.py"), []byte(existing), 0o644); err != nil {
		t.Fatalf("write conftest: %v", err)
	}

	r := NewRouter(Config{})
	if err := r.Run([]string{"init"}); err != nil {
		t.Fatalf("init returned error: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(d, "conftest.py"))
	if err != nil {
		t.Fatalf("read conftest: %v", err)
	}
	if string(got) != existing {
		t.Errorf("existing conftest.py was clobbered:\n%s", got)
	}
	// The reference copy still lands in the tool-managed shim dir.
	if _, err := os.Stat(filepath.Join(d, ".wand", "shims", "python", "conftest.py")); err != nil {
		t.Errorf("expected reference conftest in shim dir: %v", err)
	}
}

func TestResolveCaptureTargets(t *testing.T) {
	d := t.TempDir()
	mk := func(rel string) {
		p := filepath.Join(d, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(""), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mk("tests/test_foo.py")               // test file
	mk("tests/helper.py")                 // not a test file — excluded
	mk("tests/sub/test_bar.py")           // nested test file
	mk("tests/node_modules/test_skip.py") // vendored tree — skipped
	mk("main.go")                         // not a test file

	t.Chdir(d)

	t.Run("directory walks to test files only", func(t *testing.T) {
		got, err := resolveCaptureTargets([]string{"tests"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := map[string]bool{
			filepath.Join("tests", "test_foo.py"):        true,
			filepath.Join("tests", "sub", "test_bar.py"): true,
		}
		if len(got) != len(want) {
			t.Fatalf("got %v, want the %d files in %v", got, len(want), want)
		}
		for _, g := range got {
			if !want[g] {
				t.Errorf("unexpected resolved file %q", g)
			}
		}
	})

	t.Run("explicit non-test file passes through", func(t *testing.T) {
		got, err := resolveCaptureTargets([]string{"main.go"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 || got[0] != "main.go" {
			t.Fatalf("got %v, want [main.go]", got)
		}
	})

	t.Run("missing path errors and names the path", func(t *testing.T) {
		_, err := resolveCaptureTargets([]string{"tests", "does_not_exist.py"})
		if err == nil {
			t.Fatal("expected an error for a missing path, got nil")
		}
		if !strings.Contains(err.Error(), "does_not_exist.py") {
			t.Errorf("error should name the missing path, got: %v", err)
		}
	})
}
