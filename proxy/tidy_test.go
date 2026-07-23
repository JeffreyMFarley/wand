package proxy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAccessLogRoundTripAndClear(t *testing.T) {
	store := NewStoreWithRoot(t.TempDir())

	if err := store.AppendAccess(Access{Service: "s3", Hash: "aaaa"}); err != nil {
		t.Fatalf("AppendAccess: %v", err)
	}
	if err := store.AppendAccess(Access{Service: "s3", Hash: "bbbb", Missing: true}); err != nil {
		t.Fatalf("AppendAccess: %v", err)
	}

	got, err := store.LoadAccess()
	if err != nil {
		t.Fatalf("LoadAccess: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 access records, got %d", len(got))
	}
	if got[1].Hash != "bbbb" || !got[1].Missing {
		t.Fatalf("miss flag not round-tripped: %+v", got[1])
	}

	if err := store.ClearAccess(); err != nil {
		t.Fatalf("ClearAccess: %v", err)
	}
	after, err := store.LoadAccess()
	if err != nil {
		t.Fatalf("LoadAccess after clear: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("expected empty access log after clear, got %d", len(after))
	}
	// Clearing an already-absent log is a no-op, not an error.
	if err := store.ClearAccess(); err != nil {
		t.Fatalf("ClearAccess on missing file should be nil, got %v", err)
	}
}

func TestRemoveDeletesFixturePairAndIsIdempotent(t *testing.T) {
	tempDir := t.TempDir()
	store := NewStoreWithRoot(tempDir)

	if err := store.Write("s3", "abcd", []byte("req"), []byte("resp")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := store.Remove("s3", "abcd"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	for _, suffix := range []string{"_req.jsonl", "_resp.jsonl"} {
		p := filepath.Join(tempDir, "__fixtures__", "s3", "abcd"+suffix)
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("expected %s removed, stat err=%v", p, err)
		}
	}
	// Removing an already-absent pair is a no-op, not an error.
	if err := store.Remove("s3", "abcd"); err != nil {
		t.Fatalf("Remove on missing pair should be nil, got %v", err)
	}
}

func TestReachedSetExcludesMisses(t *testing.T) {
	accesses := []Access{
		{Service: "s3", Hash: "aaaa"},
		{Service: "s3", Hash: "aaaa"}, // duplicate hit collapses
		{Service: "cloudwatch", Hash: "bbbb"},
		{Service: "s3", Hash: "cccc", Missing: true},
		{Service: "s3", Hash: "cccc", Missing: true}, // duplicate miss collapses
	}
	reached, misses := reachedSet(accesses)

	if misses != 1 {
		t.Fatalf("expected 1 distinct miss, got %d", misses)
	}
	if len(reached) != 2 {
		t.Fatalf("expected 2 reached keys, got %d", len(reached))
	}
	if !reached[fixtureKey("s3", "aaaa")] || !reached[fixtureKey("cloudwatch", "bbbb")] {
		t.Fatalf("reached set missing expected keys: %v", reached)
	}
	if reached[fixtureKey("s3", "cccc")] {
		t.Fatalf("missing fixture must not be counted as reached")
	}
}

// A reachability sweep computed from the access log should flag exactly the
// stored fixtures the run never touched.
func TestOrphanComputationFromAccessLog(t *testing.T) {
	tempDir := t.TempDir()
	store := NewStoreWithRoot(tempDir)

	for _, h := range []string{"live1", "live2", "dead1"} {
		if err := store.Write("s3", h, []byte("req"), []byte("resp")); err != nil {
			t.Fatalf("Write %s: %v", h, err)
		}
	}
	// Only the two live fixtures were hit this run.
	_ = store.AppendAccess(Access{Service: "s3", Hash: "live1"})
	_ = store.AppendAccess(Access{Service: "s3", Hash: "live2"})

	accesses, err := store.LoadAccess()
	if err != nil {
		t.Fatalf("LoadAccess: %v", err)
	}
	reached, misses := reachedSet(accesses)
	if misses != 0 {
		t.Fatalf("expected no misses, got %d", misses)
	}

	refs, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var orphans []string
	for _, ref := range refs {
		if !reached[fixtureKey(ref.Service, ref.Hash)] {
			orphans = append(orphans, ref.Hash)
		}
	}
	if len(orphans) != 1 || orphans[0] != "dead1" {
		t.Fatalf("expected [dead1] orphaned, got %v", orphans)
	}
}
