package proxy

import (
	"bufio"
	"context"
	_ "embed" // for the //go:embed prompt directives below
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// This file implements the Claude-powered CLI commands. They are all
// developer-facing and never run in ci mode. Each isolates its Claude call so
// the surrounding plumbing (git, fixture I/O) works and can be inspected even
// without an API key; only the semantic step needs credentials.

// ---------------------------------------------------------------------------
// prompts — every Claude system prompt (and any static instruction text) lives
// in proxy/prompts/*.md and is embedded at build time, so they can be tuned as
// plain prose without touching Go source. The commands below assemble the
// per-call user message from these plus the relevant diff/fixture context.
// ---------------------------------------------------------------------------

var (
	// scaffold: generate one new test in the repo's style.
	//go:embed prompts/scaffold.md
	scaffoldSystemPrompt string

	// capture --from-diff: infer which tests to re-capture from the git diff.
	//go:embed prompts/capture_from_diff.md
	captureFromDiffSystemPrompt string

	// capture --name: name a captured fixture.
	//go:embed prompts/capture_name.md
	captureNameSystemPrompt string

	// diff: summarize fixture changes for a PR description.
	//go:embed prompts/diff.md
	diffSystemPrompt string

	// doctor: classify a fixture-vs-live divergence.
	//go:embed prompts/doctor.md
	doctorSystemPrompt string

	// explain: describe what scenario a fixture covers.
	//go:embed prompts/explain.md
	explainSystemPrompt string

	// explain: instruction appended to the request/response context as the user message.
	//go:embed prompts/explain_instruction.md
	explainInstruction string
)

// init trims the embedded prompts once at startup: files carry a trailing
// newline (and editors may add leading/trailing whitespace as they're tuned),
// which we don't want leaking into system/user messages.
func init() {
	for _, p := range []*string{
		&scaffoldSystemPrompt,
		&captureFromDiffSystemPrompt,
		&captureNameSystemPrompt,
		&diffSystemPrompt,
		&doctorSystemPrompt,
		&explainSystemPrompt,
		&explainInstruction,
	} {
		*p = strings.TrimSpace(*p)
	}
}

// ---------------------------------------------------------------------------
// scaffold — generate a new test matching the repo's style, queue a capture
// ---------------------------------------------------------------------------

func (r *Router) runScaffold(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: wand scaffold <description>")
	}
	desc := strings.Join(args, " ")

	examples := sampleTests(3)
	system := scaffoldSystemPrompt

	var b strings.Builder
	if len(examples) > 0 {
		b.WriteString("Existing tests for style reference:\n\n")
		for _, ex := range examples {
			fmt.Fprintf(&b, "=== %s ===\n%s\n\n", ex.path, truncateStr(ex.content, 3000))
		}
	} else {
		b.WriteString("No existing tests were found; use idiomatic style for the repo's language.\n\n")
	}
	fmt.Fprintf(&b, "Write a new test for this scenario:\n%s\n", desc)

	out, err := NewClaudeClient().Complete(context.Background(), system, b.String())
	if err != nil {
		return err
	}

	path, code := parseScaffold(out)
	if path == "" {
		// Claude didn't propose a path — show the code and let the developer place it.
		fmt.Println("Generated test (no path proposed — save it yourself):")
		fmt.Println()
		fmt.Println(code)
		return nil
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("refusing to overwrite existing file %s; the generated test is:\n\n%s", path, code)
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(path, []byte(code+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n\n", path)
	fmt.Println("Next: capture fixtures for it by running the test in capture mode, e.g.:")
	fmt.Printf("  WAND_MODE=capture <your test runner> %s\n", path)
	fmt.Println("Then name the fixtures:  wand capture --name")
	return nil
}

// ---------------------------------------------------------------------------
// capture — resolve explicit file/directory targets + post-capture naming
// ---------------------------------------------------------------------------

func (r *Router) runCapture(args []string) error {
	if len(args) >= 1 && args[0] == "--name" {
		return r.captureName()
	}
	if len(args) >= 1 && args[0] == "--from-diff" {
		return r.captureFromDiff(strings.Join(args[1:], " "))
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: wand capture <file-or-dir>... | --from-diff [description] | --name")
	}

	files, err := resolveCaptureTargets(args)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		fmt.Println("no test files found in the given paths")
		return nil
	}
	printCaptureInstructions(files)
	return nil
}

// resolveCaptureTargets validates each path and expands directories into the
// test files they contain. Explicit file arguments are taken as-is; directories
// are walked and filtered to recognized test files (skipping vendored/generated
// trees). It errors if any path does not exist, so a typo never silently
// captures nothing.
func resolveCaptureTargets(paths []string) ([]string, error) {
	var files, missing []string
	seen := map[string]bool{}
	add := func(p string) {
		p = filepath.Clean(p)
		if !seen[p] {
			seen[p] = true
			files = append(files, p)
		}
	}

	for _, p := range paths {
		info, err := os.Stat(p)
		if os.IsNotExist(err) {
			missing = append(missing, p)
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			add(p) // explicit file — taken as given, even if unusual
			continue
		}
		walkErr := filepath.WalkDir(p, func(wp string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", "node_modules", "vendor", "__fixtures__", "dist", "build":
					return filepath.SkipDir
				}
				return nil
			}
			if isTestFile(d.Name()) {
				add(wp)
			}
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("path(s) not found: %s", strings.Join(missing, ", "))
	}
	return files, nil
}

// captureFromDiff is the agentic escape hatch: instead of naming files
// explicitly, Claude reads the working-tree diff (plus an optional description)
// and proposes which existing tests exercise the changed code. It never
// captures blindly — the developer confirms the proposed scope first, since
// scope creep produces large, unreviewable fixture commits.
func (r *Router) captureFromDiff(desc string) error {
	diff, _ := codeDiff()
	if strings.TrimSpace(diff) == "" {
		fmt.Println("no working-tree changes detected; nothing to infer a capture scope from")
		return nil
	}
	index, _ := NewStore().LoadIndex()

	var b strings.Builder
	if desc != "" {
		fmt.Fprintf(&b, "Change description: %s\n\n", desc)
	}
	if len(index) > 0 {
		b.WriteString("Known fixtures (hash → tests that use them):\n")
		for h, e := range index {
			fmt.Fprintf(&b, "  %s: %s\n", h, strings.Join(e.Tests, ", "))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Git diff:\n%s\n", truncateStr(diff, 8000))

	out, err := NewClaudeClient().Complete(context.Background(), captureFromDiffSystemPrompt, b.String())
	if err != nil {
		return err
	}
	tests := parseTestList(out)
	if len(tests) == 0 {
		fmt.Println("Claude did not identify any tests to capture. Raw response:")
		fmt.Println(out)
		return nil
	}

	fmt.Println("Claude proposes capturing fixtures for these tests:")
	for _, t := range tests {
		fmt.Printf("  - %s\n", t)
	}
	fmt.Println()
	if !confirm("Proceed with this scope?") {
		fmt.Println("aborted; no fixtures captured")
		return nil
	}
	printCaptureInstructions(tests)
	return nil
}

// captureName fills in human-readable scenario names for any fixtures whose
// index entry has none — the post-capture Claude step from the design.
func (r *Router) captureName() error {
	store := NewStore()
	refs, err := store.List()
	if err != nil {
		return err
	}
	index, err := store.LoadIndex()
	if err != nil {
		return err
	}

	client := NewClaudeClient()
	system := captureNameSystemPrompt
	named := 0
	for _, ref := range refs {
		entry := index[ref.Hash]
		if entry.Scenario != "" {
			continue
		}
		req, resp, err := store.Read(ref.Service, ref.Hash)
		if err != nil {
			continue
		}
		prompt := fmt.Sprintf("Service: %s\n\nRequest:\n%s\n\nResponse:\n%s\n\nGive a short scenario name.",
			ref.Service, truncate(req, 3000), truncate(resp, 3000))
		name, err := client.Complete(context.Background(), system, prompt)
		if err != nil {
			return err
		}
		entry.Scenario = firstLine(name)
		entry.Service = ref.Service
		if entry.Captured == "" {
			entry.Captured = time.Now().Format("2006-01-02")
		}
		if entry.CapturedBy == "" {
			entry.CapturedBy = "wand/1.0.0"
		}
		if entry.RequestSummary == "" {
			entry.RequestSummary = truncate(req, 200)
		}
		index[ref.Hash] = entry
		named++
		fmt.Printf("named %s: %s\n", ref.Hash, entry.Scenario)
	}
	if named == 0 {
		fmt.Println("all fixtures already have scenario names")
		return nil
	}
	return store.WriteIndex(index)
}

// ---------------------------------------------------------------------------
// diff — plain-English summary of changed fixtures, for a PR description
// ---------------------------------------------------------------------------

func (r *Router) runDiff(args []string) error {
	var diff string
	var err error
	if len(args) >= 2 && args[0] == "--pr" {
		diff, err = ghPRDiff(args[1])
	} else {
		diff, err = fixtureDiff()
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(diff) == "" {
		fmt.Println("no fixture changes detected")
		return nil
	}

	system := diffSystemPrompt
	out, err := NewClaudeClient().Complete(context.Background(), system,
		"Summarize what changed in these fixtures:\n\n"+truncateStr(diff, 12000))
	if err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}

// ---------------------------------------------------------------------------
// doctor — classify recorded livetest divergences
// ---------------------------------------------------------------------------

func (r *Router) runDoctor(args []string) error {
	_ = args
	store := NewStore()
	divs, err := store.LoadDivergences()
	if err != nil {
		return err
	}
	if len(divs) == 0 {
		fmt.Println("no livetest divergences recorded.")
		fmt.Println("run your tests with WAND_MODE=livetest first, then re-run doctor.")
		return nil
	}

	client := NewClaudeClient()
	system := doctorSystemPrompt

	breaking := 0
	for _, d := range divs {
		prompt := fmt.Sprintf("Service: %s\n\nStored fixture response:\n%s\n\nLive response:\n%s\n",
			d.Service, truncateStr(d.Fixture, 3000), truncateStr(d.Live, 3000))
		out, err := client.Complete(context.Background(), system, prompt)
		if err != nil {
			return err
		}
		class, reason := splitClass(out)
		if class == "BREAKING" {
			breaking++
		}
		fmt.Printf("[%-8s] %s %s — %s\n", class, d.Service, d.Hash, reason)
	}

	fmt.Printf("\n%d divergence(s) classified; %d breaking.\n", len(divs), breaking)
	if breaking > 0 {
		return fmt.Errorf("%d breaking divergence(s) found", breaking)
	}
	return nil
}

// ---------------------------------------------------------------------------
// explain — describe what scenario a fixture covers
// ---------------------------------------------------------------------------

func (r *Router) runExplain(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: wand explain <hash>")
	}
	store := NewStore()
	ref, ok := store.Resolve(args[0])
	if !ok {
		return fmt.Errorf("no fixture found matching hash %q", args[0])
	}
	req, resp, err := store.Read(ref.Service, ref.Hash)
	if err != nil {
		return fmt.Errorf("reading fixture: %w", err)
	}
	index, _ := store.LoadIndex()
	entry := index[ref.Hash]

	var b strings.Builder
	fmt.Fprintf(&b, "Service: %s\nHash: %s\n", ref.Service, ref.Hash)
	if len(entry.Tests) > 0 {
		fmt.Fprintf(&b, "Used by tests: %s\n", strings.Join(entry.Tests, ", "))
	}
	fmt.Fprintf(&b, "\nRequest:\n%s\n\nResponse:\n%s\n", truncate(req, 4000), truncate(resp, 4000))
	b.WriteString("\n" + explainInstruction)

	system := explainSystemPrompt
	out, err := NewClaudeClient().Complete(context.Background(), system, b.String())
	if err != nil {
		return err
	}
	fmt.Printf("Fixture %s (%s)\n\n%s\n", ref.Hash, ref.Service, out)
	return nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

type testSample struct {
	path    string
	content string
}

// sampleTests walks the working tree and returns up to n test files as style
// references, skipping vendored and generated directories.
func sampleTests(n int) []testSample {
	var out []testSample
	_ = filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor", "__fixtures__", "dist", "build":
				return filepath.SkipDir
			}
			return nil
		}
		if len(out) >= n {
			return filepath.SkipAll
		}
		if isTestFile(d.Name()) {
			if data, err := os.ReadFile(p); err == nil {
				out = append(out, testSample{path: p, content: string(data)})
			}
		}
		return nil
	})
	return out
}

func isTestFile(name string) bool {
	switch {
	case strings.HasSuffix(name, "_test.go"),
		strings.HasSuffix(name, "_test.py"),
		strings.HasPrefix(name, "test_") && strings.HasSuffix(name, ".py"),
		strings.HasSuffix(name, ".test.ts"),
		strings.HasSuffix(name, ".test.js"),
		strings.HasSuffix(name, ".spec.ts"),
		strings.HasSuffix(name, ".spec.js"):
		return true
	}
	return false
}

func parseScaffold(out string) (path, code string) {
	out = strings.TrimSpace(out)
	rest := out
	if lines := strings.SplitN(out, "\n", 2); len(lines) > 0 {
		first := strings.TrimSpace(lines[0])
		if strings.HasPrefix(strings.ToLower(first), "path:") {
			path = strings.TrimSpace(first[len("path:"):])
			if len(lines) > 1 {
				rest = lines[1]
			} else {
				rest = ""
			}
		}
	}
	return path, stripFences(rest)
}

func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		if j := strings.LastIndex(s, "```"); j >= 0 {
			s = s[:j]
		}
	}
	return strings.TrimSpace(s)
}

func printCaptureInstructions(targets []string) {
	fmt.Println("Capture scope:")
	for _, t := range targets {
		fmt.Printf("  - %s\n", t)
	}
	fmt.Println("\nRun your test suite in capture mode to record fixtures, e.g.:")
	fmt.Printf("  WAND_MODE=capture <your test runner> %s\n", strings.Join(targets, " "))
	fmt.Println("Then name the captured fixtures:  wand capture --name")
}

// parseTestList extracts a list of test identifiers from Claude's response,
// preferring an embedded JSON array and falling back to one-per-line bullets.
func parseTestList(s string) []string {
	s = stripFences(s)
	if start, end := strings.Index(s, "["), strings.LastIndex(s, "]"); start >= 0 && end > start {
		var arr []string
		if json.Unmarshal([]byte(s[start:end+1]), &arr) == nil && len(arr) > 0 {
			return arr
		}
	}
	var out []string
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "-*• "))
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func confirm(prompt string) bool {
	fmt.Printf("%s [y/N]: ", prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

func splitClass(out string) (class, reason string) {
	out = strings.TrimSpace(out)
	lines := strings.SplitN(out, "\n", 2)
	first := strings.ToUpper(strings.TrimSpace(strings.Trim(lines[0], "*#- ")))
	for _, known := range []string{"BREAKING", "BENIGN", "NOISE"} {
		if strings.HasPrefix(first, known) {
			class = known
			break
		}
	}
	if class == "" {
		class = "UNKNOWN"
	}
	if len(lines) > 1 {
		reason = strings.TrimSpace(lines[1])
	} else {
		reason = strings.TrimSpace(strings.TrimPrefix(first, class))
	}
	return class, reason
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(strings.Trim(s, "\"'"))
}

func truncate(b []byte, n int) string { return truncateStr(string(b), n) }

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…(truncated)"
}

// fixturesPath returns the configured fixture directory, defaulting to __fixtures__.
func fixturesPath() string {
	if data, err := os.ReadFile("wand.yaml"); err == nil {
		var cfg struct {
			Fixtures struct {
				Path string `yaml:"path"`
			} `yaml:"fixtures"`
		}
		if yaml.Unmarshal(data, &cfg) == nil && cfg.Fixtures.Path != "" {
			return cfg.Fixtures.Path
		}
	}
	return "__fixtures__"
}

func fixtureDiff() (string, error) {
	out, err := exec.Command("git", "diff", "HEAD", "--", fixturesPath()).Output()
	return string(out), gitErr(err)
}

func codeDiff() (string, error) {
	out, err := exec.Command("git", "diff", "HEAD").Output()
	return string(out), gitErr(err)
}

func ghPRDiff(number string) (string, error) {
	out, err := exec.Command("gh", "pr", "diff", number).Output()
	if err != nil {
		return "", fmt.Errorf("gh pr diff %s failed (is the gh CLI installed and authenticated?): %w", number, err)
	}
	return string(out), nil
}

// gitErr treats a non-zero git exit as an empty diff rather than a hard error,
// so `diff` on a clean tree reports "no changes" instead of failing.
func gitErr(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*exec.ExitError); ok {
		return nil
	}
	return err
}
