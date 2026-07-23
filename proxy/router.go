package proxy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"wand/shims"
)

// Config holds basic routing settings for the CLI entry points.
type Config struct {
	Mode    string
	Service string
}

// Router dispatches CLI commands to the appropriate workflow.
type Router struct {
	cfg Config
}

func NewRouter(cfg Config) *Router {
	return &Router{cfg: cfg}
}

const usageText = `wand — language-agnostic API mocking proxy

Usage:
  wand <command> [args]

Commands:
  init                        Scaffold wand.yaml and install shims for detected languages
  proxy start|stop            Start or stop the proxy sidecar
  capture <file-or-dir>...    Print the capture command for the given test files/dirs
  capture --from-diff [desc]  Claude infers the capture scope from the git diff
  capture --name              Name captured fixtures and update index.json (post-capture)
  diff [--pr <number>]        Semantic diff of changed fixtures (Claude summary)
  doctor                      livetest all fixtures and classify divergences
  tidy [--force] [--reset]    Delete fixtures unreached by the last ci-mode run
  verify                      ci-mode dry run; report any fixture misses
  explain <hash>              Describe what scenario a fixture covers
  scan <file-or-dir>...       Report which source files would get tests (generates nothing)
  scaffold <file-or-dir>...   Generate integration tests for the given source files/dirs
  normalizer                  Run normalization discovery/checks
  help                        Show this help

Environment:
  WAND_MODE   ci (default) | capture | passthrough | livetest
  WAND_PORT   proxy listen port (default 8877)
`

func (r *Router) Run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usageText)
		return nil
	}

	command := strings.ToLower(args[0])
	remaining := args[1:]

	switch command {
	case "help", "-h", "--help":
		fmt.Print(usageText)
		return nil
	case "init":
		return r.runInit(remaining)
	case "proxy":
		return r.runProxy(remaining)
	case "capture":
		return r.runCapture(remaining)
	case "diff":
		return r.runDiff(remaining)
	case "doctor":
		return r.runDoctor(remaining)
	case "tidy":
		return r.runTidy(remaining)
	case "explain":
		return r.runExplain(remaining)
	case "scan":
		return r.runScan(remaining)
	case "scaffold":
		return r.runScaffold(remaining)
	case "verify":
		return r.runVerify(remaining)
	case "normalizer":
		return r.runNormalizer(remaining)
	default:
		return fmt.Errorf("unknown command %q (run 'wand help' for usage)", command)
	}
}

const defaultWandConfig = `name: wand

proxy:
  port: 8877
  mode: ${WAND_MODE:-ci}

services:
  - name: http
    upstream_url: http://127.0.0.1:8080

fixtures:
  path: __fixtures__
  index: __fixtures__/index.json

claude:
  model: claude-sonnet-4-6
  # api key read from ANTHROPIC_API_KEY
`

func (r *Router) runInit(args []string) error {
	_ = args
	root := "."
	configPath := filepath.Join(root, "wand.yaml")
	if _, err := os.Stat(configPath); err == nil {
		// Never clobber an existing config — it holds service upstreams the
		// proxy needs. Re-running init should be a safe no-op for the config.
		fmt.Println("wand.yaml already exists; leaving it unchanged")
	} else if os.IsNotExist(err) {
		if err := os.WriteFile(configPath, []byte(defaultWandConfig), 0o644); err != nil {
			return err
		}
		fmt.Println("created wand.yaml")
	} else {
		return err
	}

	// Shims are tool-managed code, so installing them is independent of whether
	// the config already existed — re-running init on a python project keeps the
	// shims in sync with the installed wand version.
	return r.installPythonShims(root)
}

// installPythonShims drops the python client-side shims into the project when a
// python project is detected. client.py and marks.py are always installed; the
// boto3 bridge only when boto3/botocore is a declared dependency, so non-AWS
// projects don't get an unused module.
func (r *Router) installPythonShims(root string) error {
	if !isPythonProject(root) {
		return nil
	}

	modules := []string{"client.py", "marks.py"}
	boto3 := usesBoto3(root)
	if boto3 {
		modules = append(modules, "boto3_shim.py")
	}

	// Hidden, tool-managed directory — overwritten on each init, not hand-edited.
	dest := filepath.Join(root, ".wand", "shims", "python")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	for _, name := range modules {
		data, err := shims.PythonShim(name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dest, name), data, 0o644); err != nil {
			return err
		}
	}

	fmt.Printf("installed python shims to %s: %s\n", dest, strings.Join(modules, ", "))

	// The boto3 bridge only records/replays while boto3_shim.intercept() is
	// active. Drop a root conftest.py that enters it for the whole session so
	// `WAND_MODE=capture pytest` actually produces fixtures. Only relevant for
	// boto3 projects, and never clobbered — a project's own conftest may hold
	// unrelated fixtures.
	if boto3 {
		if err := installPytestConftest(root, dest); err != nil {
			return err
		}
	}
	return nil
}

// installPytestConftest writes the session-activation conftest to the project
// root, unless one already exists (a project may have its own). A reference
// copy always lands in the tool-managed shim dir so users with their own
// conftest have something to copy the _wand_intercept fixture from.
func installPytestConftest(root, shimDir string) error {
	data, err := shims.PythonConftest()
	if err != nil {
		return err
	}

	reference := filepath.Join(shimDir, "conftest.py")
	if err := os.WriteFile(reference, data, 0o644); err != nil {
		return err
	}

	conftestPath := filepath.Join(root, "conftest.py")
	if _, err := os.Stat(conftestPath); err == nil {
		fmt.Printf("conftest.py already exists; leaving it unchanged — add the "+
			"_wand_intercept fixture from %s to activate the boto3 bridge\n", reference)
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.WriteFile(conftestPath, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("created %s (activates the boto3 bridge for pytest)\n", conftestPath)
	return nil
}

// isPythonProject reports whether root looks like a python project: a standard
// packaging/dependency manifest, or failing that any top-level .py file.
func isPythonProject(root string) bool {
	markers := []string{"pyproject.toml", "setup.py", "setup.cfg", "requirements.txt", "Pipfile"}
	for _, m := range markers {
		if _, err := os.Stat(filepath.Join(root, m)); err == nil {
			return true
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".py") {
			return true
		}
	}
	return false
}

// usesBoto3 reports whether any dependency manifest mentions boto3 or botocore.
func usesBoto3(root string) bool {
	manifests := []string{"requirements.txt", "pyproject.toml", "Pipfile", "setup.py", "setup.cfg"}
	for _, name := range manifests {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		lower := strings.ToLower(string(data))
		if strings.Contains(lower, "boto3") || strings.Contains(lower, "botocore") {
			return true
		}
	}
	return false
}

func (r *Router) runProxy(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: wand proxy <start|stop>")
	}

	subcommand := strings.ToLower(args[0])
	switch subcommand {
	case "start":
		if os.Getenv("WAND_NONBLOCKING") != "" {
			fmt.Printf("starting wand proxy on port %d (non-blocking)\n", 8877)
			return nil
		}
		server := NewServer(r.cfg)
		fmt.Printf("starting wand proxy on port %d\n", server.Port)
		return server.Start()
	case "stop":
		fmt.Println("proxy sidecar placeholder: stopped")
	default:
		return fmt.Errorf("unknown proxy command %q", subcommand)
	}
	return nil
}

func (r *Router) runVerify(args []string) error {
	_ = args
	fmt.Println("verify placeholder: CI mode report")
	return nil
}

func (r *Router) runNormalizer(args []string) error {
	fmt.Println("normalizer mode placeholder")
	_ = args
	return nil
}
