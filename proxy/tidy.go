package proxy

import (
	"fmt"
)

// ---------------------------------------------------------------------------
// tidy — garbage-collect fixtures unreached by the last test run
// ---------------------------------------------------------------------------
//
// Fixtures are content-addressed and capture is purely additive, so when a
// request shape changes the old fixture is orphaned but never removed. tidy is
// the reachability sweep that reclaims them: the proxy marks every fixture it
// hits during a run (the access log), and tidy deletes any stored fixture the
// run never touched. It replays in ci mode, so no live credentials are needed.

func fixtureKey(service, hash string) string { return service + "/" + hash }

// reachedSet returns the set of "service/hash" keys hit during the run and the
// count of distinct fixtures that were requested but absent. A miss is reported
// as a coverage hint, not a hard error: application code often makes best-effort
// API calls and catches the absence, so a miss can coexist with a green suite.
func reachedSet(accesses []Access) (map[string]bool, int) {
	reached := map[string]bool{}
	missing := map[string]bool{}
	for _, a := range accesses {
		if a.Missing {
			missing[fixtureKey(a.Service, a.Hash)] = true
			continue
		}
		reached[fixtureKey(a.Service, a.Hash)] = true
	}
	return reached, len(missing)
}

func (r *Router) runTidy(args []string) error {
	force := false
	reset := false
	for _, a := range args {
		switch a {
		case "--force", "-f":
			force = true
		case "--reset":
			reset = true
		default:
			return fmt.Errorf("unknown tidy flag %q (usage: wand tidy [--force] [--reset])", a)
		}
	}

	store := NewStore()

	// --reset clears the log so the next full-suite run marks reachability from
	// a clean slate — otherwise accesses from before a code change linger and
	// keep now-dead fixtures marked as live.
	if reset {
		if err := store.ClearAccess(); err != nil {
			return err
		}
		fmt.Println("access log cleared; run your full test suite (ci mode), then run 'wand tidy'.")
		return nil
	}

	accesses, err := store.LoadAccess()
	if err != nil {
		return err
	}
	if len(accesses) == 0 {
		fmt.Println("no fixture accesses recorded.")
		fmt.Println("run your full test suite (WAND_MODE=ci) first, then re-run tidy.")
		return nil
	}

	reached, misses := reachedSet(accesses)

	refs, err := store.List()
	if err != nil {
		return err
	}

	var orphans []FixtureRef
	for _, ref := range refs {
		if !reached[fixtureKey(ref.Service, ref.Hash)] {
			orphans = append(orphans, ref)
		}
	}

	index, _ := store.LoadIndex()

	if len(orphans) == 0 {
		fmt.Printf("all %d fixture(s) were reached; nothing to tidy.\n", len(refs))
		if misses > 0 {
			fmt.Printf("(heads-up: %d fixture(s) were requested but absent — capture them if they matter.)\n", misses)
		}
		return nil
	}

	fmt.Printf("%d of %d fixture(s) unreached by the last run:\n\n", len(orphans), len(refs))
	for _, o := range orphans {
		scenario := index[o.Hash].Scenario
		if scenario == "" {
			scenario = "(unnamed)"
		}
		fmt.Printf("  %s  %s  %s\n", o.Service, o.Hash, scenario)
	}

	// Misses are a coverage hint, not a blocker: code that makes best-effort API
	// calls and catches the absence produces misses on a perfectly green suite.
	// The real safeguard against deleting live fixtures is a full, passing run
	// plus the dry-run/--force gate below — not the presence of misses.
	if misses > 0 {
		fmt.Printf("\nheads-up: %d fixture(s) were requested but absent during the run.\n", misses)
		fmt.Println("these are calls your code makes with no stored fixture — capture them if they matter.")
	}

	if !force {
		fmt.Printf("\nthis was a dry run. re-run 'wand tidy --force' to delete the %d orphan(s).\n", len(orphans))
		fmt.Println("only tidy after a full, passing suite run, or live fixtures may be deleted.")
		return nil
	}

	for _, o := range orphans {
		if err := store.Remove(o.Service, o.Hash); err != nil {
			return fmt.Errorf("removing %s/%s: %w", o.Service, o.Hash, err)
		}
		delete(index, o.Hash)
	}
	if err := store.WriteIndex(index); err != nil {
		return err
	}
	// Clear the log so the next tidy cycle starts from a fresh marking run.
	if err := store.ClearAccess(); err != nil {
		return err
	}
	fmt.Printf("\ndeleted %d orphan fixture(s); access log cleared.\n", len(orphans))
	return nil
}
