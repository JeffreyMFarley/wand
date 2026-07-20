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
	"sync"
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
	// scaffold runs as two calls: a cheap qualify check decides whether a source
	// file warrants an integration test, and only then does the write step
	// generate one — so non-qualifying files never pay for generation.
	//go:embed prompts/scaffold_qualify.md
	scaffoldQualifySystemPrompt string

	//go:embed prompts/scaffold_write.md
	scaffoldWriteSystemPrompt string

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
		&scaffoldQualifySystemPrompt,
		&scaffoldWriteSystemPrompt,
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
// scaffold — generate an integration test for each given source file
// ---------------------------------------------------------------------------

func (r *Router) runScaffold(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: wand scaffold <file-or-dir>...")
	}
	sources, err := resolveSourceTargets(args)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		fmt.Println("no source files found in the given paths")
		return nil
	}

	client := NewClaudeClient()
	var written, skipped int
	for _, src := range sources {
		wrote, err := r.scaffoldOne(client, src)
		if err != nil {
			return err
		}
		if wrote {
			written++
		} else {
			skipped++
		}
	}

	fmt.Printf("\nscaffold complete: %d written, %d skipped\n", written, skipped)
	if written > 0 {
		fmt.Println("Next: capture fixtures by running the new tests in capture mode, e.g.:")
		fmt.Println("  WAND_MODE=capture <your test runner> <new test files>")
		fmt.Println("Then name the fixtures:  wand capture --name")
	}
	return nil
}

// scaffoldUserMessage builds the per-file user message shared by the qualify
// and write scaffold calls: the source path plus its (truncated) contents.
func scaffoldUserMessage(src string, data []byte) string {
	return fmt.Sprintf("Source file: %s\n\n%s\n", src, truncateStr(string(data), 12000))
}

// completer is the slice of ClaudeClient the scaffold/scan flows depend on.
// Narrowing to an interface lets tests substitute a stub verdict source without
// real API calls or credentials.
type completer interface {
	Complete(ctx context.Context, system, user string) (string, error)
}

// qualifyForTest runs the cheap classification call for one source file,
// reporting whether it warrants an integration test and, when it does not, the
// one-line reason from the prompt. Shared by `wand scan` and `wand scaffold` so
// both classify identically.
func qualifyForTest(ctx context.Context, client completer, user string) (qualifies bool, reason string, err error) {
	verdict, err := client.Complete(ctx, scaffoldQualifySystemPrompt, user)
	if err != nil {
		return false, "", err
	}
	if reason, skip := noIntegrationTest(verdict); skip {
		return false, reason, nil
	}
	return true, "", nil
}

// ---------------------------------------------------------------------------
// scan — report which source files would get tests, without generating any
// ---------------------------------------------------------------------------

// runScan classifies each source file with the qualify prompt and prints a
// report of what `wand scaffold` would do, generating nothing. It's the
// read-only preview of a scaffold sweep: which files qualify, where their tests
// would land, and which already have one.
func (r *Router) runScan(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: wand scan <file-or-dir>...")
	}
	sources, err := resolveSourceTargets(args)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		fmt.Println("no source files found in the given paths")
		return nil
	}

	client := NewClaudeClient()
	fmt.Printf("scanning %d source file(s) with %s...\n", len(sources), client.Model())

	// Live counter, overwritten in place, so a long sweep shows progress
	// instead of appearing hung. Results land out of order (concurrent), so a
	// running count is clearer than per-file lines.
	results, err := scanSources(client, sources, func(done, total int, _ scanClassification) {
		fmt.Printf("\r  %d/%d scanned", done, total)
	})
	fmt.Println() // finish the progress line before the report (or an error)
	if err != nil {
		return err
	}
	fmt.Println()

	var qualifying, existing, skipped []string
	for _, res := range results {
		switch {
		case !res.qualifies:
			skipped = append(skipped, fmt.Sprintf("  %s: %s", res.src, res.reason))
		case res.exists:
			existing = append(existing, fmt.Sprintf("  %s -> %s", res.src, res.dest))
		default:
			qualifying = append(qualifying, fmt.Sprintf("  %s -> %s", res.src, res.dest))
		}
	}

	if len(qualifying) > 0 {
		fmt.Printf("WOULD GENERATE (%d):\n%s\n\n", len(qualifying), strings.Join(qualifying, "\n"))
	}
	if len(existing) > 0 {
		fmt.Printf("QUALIFIES, TEST ALREADY EXISTS (%d):\n%s\n\n", len(existing), strings.Join(existing, "\n"))
	}
	if len(skipped) > 0 {
		fmt.Printf("SKIP (%d):\n%s\n\n", len(skipped), strings.Join(skipped, "\n"))
	}

	fmt.Printf("scan complete: %d would get tests, %d already have one, %d skipped\n",
		len(qualifying), len(existing), len(skipped))
	if len(qualifying) > 0 {
		fmt.Println("Run 'wand scaffold <paths>' to generate them.")
	}
	return nil
}

// scanClassification is one source file's verdict from a scan sweep.
type scanClassification struct {
	src       string
	qualifies bool
	reason    string // why it was skipped, when !qualifies
	dest      string // where the test would land, when qualifies
	exists    bool   // whether that test file already exists, when qualifies
}

// scanConcurrency bounds how many qualify calls are in flight at once. A scan of
// a large tree is otherwise one sequential API round-trip per file, which is far
// too slow; a small pool keeps it well within the API's rate limits while
// cutting wall-clock time by roughly this factor.
const scanConcurrency = 8

// classifyForScan runs the qualify call for one source file and records where
// its test would land and whether one already exists. Only I/O and API errors
// are returned; a file that doesn't qualify, or one whose extension has no
// test-path convention, comes back as a non-qualifying result with a reason
// (mirroring scaffold's per-file tolerance).
func classifyForScan(ctx context.Context, client completer, src string) (scanClassification, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return scanClassification{}, err
	}
	qualifies, reason, err := qualifyForTest(ctx, client, scaffoldUserMessage(src, data))
	if err != nil {
		return scanClassification{}, err
	}
	res := scanClassification{src: src, qualifies: qualifies, reason: reason}
	if qualifies {
		dest, err := testPathFor(src)
		if err != nil {
			// Unsupported extension (only reachable via an explicit file
			// arg): report it as a skip rather than a qualifying test.
			res.qualifies = false
			res.reason = err.Error()
		} else {
			res.dest = dest
			if _, statErr := os.Stat(dest); statErr == nil {
				res.exists = true
			}
		}
	}
	return res, nil
}

// scanSources classifies every source file, generating nothing. Files are
// classified concurrently (bounded by scanConcurrency) since each is an
// independent API round-trip, but the returned slice preserves input order.
// progress, if non-nil, is called once per successfully classified file with a
// running done count and the file's result; it is serialized, so it may print
// freely.
//
// The first I/O or API error aborts the sweep and is returned immediately: the
// shared context is cancelled, which kills in-flight `claude` calls and stops
// new ones from launching, so a failure surfaces where it happens instead of
// after every remaining file has been scanned.
func scanSources(client completer, sources []string, progress func(done, total int, res scanClassification)) ([]scanClassification, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	results := make([]scanClassification, len(sources))

	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	done := 0
	sem := make(chan struct{}, scanConcurrency)

	for i, src := range sources {
		// A prior call already failed and cancelled the sweep; don't queue
		// work we'd only abort.
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, src string) {
			defer wg.Done()
			defer func() { <-sem }()

			res, err := classifyForScan(ctx, client, src)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				// Record the first genuine failure and cancel so in-flight and
				// pending calls abort. Later errors are the cancellation
				// fallout of this one (cancel() runs under the same lock after
				// firstErr is set), so the firstErr guard keeps the real cause.
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				return
			}
			results[i] = res
			done++
			if progress != nil {
				progress(done, len(sources), res)
			}
		}(i, src)
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

// scaffoldOne generates a single integration test for one source file. It
// reports whether a test file was written (false = skipped, either because the
// file doesn't qualify per the prompt or a test already exists). Only I/O and
// API errors are returned; per-file skips are printed and swallowed so one
// unqualifying file never aborts a directory sweep.
func (r *Router) scaffoldOne(client *ClaudeClient, src string) (bool, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return false, err
	}

	dest, err := testPathFor(src)
	if err != nil {
		fmt.Printf("skipped %s: %v\n", src, err)
		return false, nil
	}
	if _, err := os.Stat(dest); err == nil {
		fmt.Printf("skipped %s: %s already exists\n", src, dest)
		return false, nil
	}

	user := scaffoldUserMessage(src, data)

	// Qualify first: a cheap classification call decides whether the file
	// warrants an integration test at all. If it doesn't, skip without paying
	// for the (much larger) generation call.
	qualifies, reason, err := qualifyForTest(context.Background(), client, user)
	if err != nil {
		return false, err
	}
	if !qualifies {
		fmt.Printf("skipped %s: %s\n", src, reason)
		return false, nil
	}

	// Qualified: generate the test file. The write prompt keeps the same
	// "No integration test." escape hatch in case a closer reading finds no
	// directly-qualifying subject after all.
	out, err := client.Complete(context.Background(), scaffoldWriteSystemPrompt, user)
	if err != nil {
		return false, err
	}
	if reason, skip := noIntegrationTest(out); skip {
		fmt.Printf("skipped %s: %s\n", src, reason)
		return false, nil
	}

	code := stripFences(out)
	if dir := filepath.Dir(dest); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return false, err
		}
	}
	if err := os.WriteFile(dest, []byte(code+"\n"), 0o644); err != nil {
		return false, err
	}
	fmt.Printf("wrote %s (from %s)\n", dest, src)
	return true, nil
}

// testPathFor derives the path of the test file to generate for a source file,
// applying each language's convention: Go tests must sit beside the source in
// the same package; Python tests are mirrored under a top-level tests/ dir;
// JS/TS tests sit beside the source with a .test infix; PHP tests sit beside the
// source with a Test suffix; Ruby specs are mirrored under a top-level spec/ dir;
// Java tests mirror src/main/java into src/test/java (when present) with a Test
// suffix, otherwise sit beside the source.
func testPathFor(src string) (string, error) {
	dir := filepath.Dir(src)
	base := filepath.Base(src)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)

	switch ext {
	case ".go":
		return filepath.Join(dir, name+"_test.go"), nil
	case ".py":
		return filepath.Join("tests", dir, "test_"+name+".py"), nil
	case ".js", ".jsx", ".ts", ".tsx":
		return filepath.Join(dir, name+".test"+ext), nil
	case ".php":
		return filepath.Join(dir, name+"Test.php"), nil
	case ".rb":
		return filepath.Join("spec", dir, name+"_spec.rb"), nil
	case ".java":
		mainDir := filepath.Join("src", "main", "java")
		testDir := filepath.Join("src", "test", "java")
		return filepath.Join(strings.Replace(dir, mainDir, testDir, 1), name+"Test.java"), nil
	default:
		return "", fmt.Errorf("unsupported source extension %q", ext)
	}
}

// noIntegrationTest detects the prompt's "No integration test. <reason>"
// response for a source file that doesn't qualify, returning the reason.
func noIntegrationTest(out string) (reason string, skip bool) {
	trimmed := strings.TrimLeft(strings.TrimSpace(out), "> ") // may arrive as a blockquote
	const marker = "No integration test"
	if !strings.HasPrefix(trimmed, marker) {
		return "", false
	}
	reason = firstLine(strings.TrimLeft(strings.TrimPrefix(trimmed, marker), ".:- "))
	if reason == "" {
		reason = "no qualifying external calls"
	}
	return reason, true
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

// resolveCaptureTargets expands the given paths into the test files to capture.
func resolveCaptureTargets(paths []string) ([]string, error) {
	return walkTargets(paths, isTestFile)
}

// resolveSourceTargets expands the given paths into the source files to
// scaffold tests for.
func resolveSourceTargets(paths []string) ([]string, error) {
	return walkTargets(paths, isSourceFile)
}

// walkTargets validates each path and expands directories into the files that
// keep() accepts. Explicit file arguments are taken as-is (keep is not applied,
// so a developer can name any file directly); directories are walked and
// filtered, skipping vendored/generated trees. It errors if any path does not
// exist, so a typo never silently resolves to nothing.
func walkTargets(paths []string, keep func(name string) bool) ([]string, error) {
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
			if keep(d.Name()) {
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

func isTestFile(name string) bool {
	if strings.HasSuffix(name, "_test.go") ||
		strings.HasSuffix(name, "_test.py") ||
		(strings.HasPrefix(name, "test_") && strings.HasSuffix(name, ".py")) ||
		strings.HasSuffix(name, "Test.php") ||
		strings.HasSuffix(name, "_spec.rb") ||
		strings.HasSuffix(name, "Test.java") {
		return true
	}
	for _, infix := range []string{".test.", ".spec."} {
		for _, ext := range []string{"js", "jsx", "ts", "tsx"} {
			if strings.HasSuffix(name, infix+ext) {
				return true
			}
		}
	}
	return false
}

// isSourceFile reports whether name is a source file scaffold can generate a
// test for: a recognized language extension that is not itself a test file.
func isSourceFile(name string) bool {
	if isTestFile(name) {
		return false
	}
	switch filepath.Ext(name) {
	case ".go", ".py", ".js", ".jsx", ".ts", ".tsx", ".php", ".rb", ".java":
		return true
	}
	return false
}

// stripFences extracts the code from Claude's response. Models often wrap the
// file in a ```lang fenced block, and sometimes precede it with a paragraph of
// preamble ("I'll analyze the file...") or the reasoning steps from the prompt
// — none of which must end up in the written file. So we locate the first
// fenced block anywhere in the response and return only its contents,
// discarding everything before the opening fence and after the closing fence.
// If there is no fence at all, the whole trimmed response is returned, since
// the model occasionally emits raw code with no fence.
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	open := strings.Index(s, "```")
	if open < 0 {
		return s
	}
	// Drop everything up to and including the opening fence's own line, which
	// carries the optional language tag (```python).
	rest := s[open+3:]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[nl+1:]
	} else {
		rest = ""
	}
	if close := strings.Index(rest, "```"); close >= 0 {
		rest = rest[:close]
	}
	return strings.TrimSpace(rest)
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
