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
		{name: "init", args: []string{"init"}, checkFile: "wand.yaml"},
		{name: "proxy start", args: []string{"proxy", "start"}},
		{name: "proxy stop", args: []string{"proxy", "stop"}},
		{name: "capture description", args: []string{"capture", "describe change"}},
		{name: "capture tests", args: []string{"capture", "--tests", "test_report.py"}},
		{name: "diff", args: []string{"diff"}},
		{name: "diff pr", args: []string{"diff", "--pr", "447"}},
		{name: "doctor", args: []string{"doctor"}},
		{name: "explain", args: []string{"explain", "abc123"}},
		{name: "scaffold", args: []string{"scaffold", "new scenario"}},
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
