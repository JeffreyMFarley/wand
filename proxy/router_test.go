package proxy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRouterRunSupportsScaffoldedCommands(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		checkFile string
	}{
		// Only offline, deterministic commands belong here. The Claude-powered
		// commands (capture <description>, diff --pr, explain, scaffold) do real
		// work needing credentials or fixtures and are covered by helper unit
		// tests plus manual end-to-end runs instead.
		{name: "init", args: []string{"init"}, checkFile: "wand.yaml"},
		{name: "help", args: []string{"help"}},
		{name: "proxy start", args: []string{"proxy", "start"}},
		{name: "proxy stop", args: []string{"proxy", "stop"}},
		{name: "capture tests", args: []string{"capture", "--tests", "test_report.py"}},
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
		})
	}
}
