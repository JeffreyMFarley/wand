package proxy

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestHashProducesHyphenatedBlake2b(t *testing.T) {
	hash := Hash([]byte("hello"))
	// 16-byte digest → 8 groups of 4 hex chars (e.g. 0b2d-c84a-…-42db).
	if matched, err := regexp.MatchString(`^[0-9a-f]{4}(?:-[0-9a-f]{4}){7}$`, hash); err != nil || !matched {
		t.Fatalf("expected 8-group hyphenated 16-byte hash, got %q", hash)
	}
}

func TestStoreRoundTripAndIndex(t *testing.T) {
	tempDir := t.TempDir()
	store := NewStoreWithRoot(tempDir)

	if err := store.Write("snowflake", "abc123", []byte("req"), []byte("resp")); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	req, resp, err := store.Read("snowflake", "abc123")
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if string(req) != "req" || string(resp) != "resp" {
		t.Fatalf("unexpected fixture contents: req=%q resp=%q", req, resp)
	}

	index, err := store.LoadIndex()
	if err != nil {
		t.Fatalf("LoadIndex returned error: %v", err)
	}
	if _, ok := index["abc123"]; !ok {
		t.Fatalf("expected index entry for hash abc123")
	}

	if _, err := os.Stat(filepath.Join(tempDir, "__fixtures__", "index.json")); err != nil {
		t.Fatalf("expected index.json to be written: %v", err)
	}
}

func TestNormalizerAppliesConfigDrivenRules(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer os.Chdir(wd)

	serviceDir := filepath.Join(wd, "..", "services")
	if err := os.Chdir(serviceDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	normalizer := NewNormalizer()
	body := []byte(`{"sessionId":"abc","requestId":"1","message":"ok","createdOn":"2025-01-02T03:04:05Z"}`)
	normalized, err := normalizer.NormalizeRequest("snowflake", body)
	if err != nil {
		t.Fatalf("NormalizeRequest returned error: %v", err)
	}
	if string(normalized) == string(body) {
		t.Fatalf("expected normalization to change payload")
	}
}

func TestNormalizerScopesTransformsToLocation(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer os.Chdir(wd)
	if err := os.Chdir(filepath.Join(wd, "..", "services")); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	normalizer := NewNormalizer()
	// dbName is at the configured location $.results[*].dbName and must be
	// stripped; userId ends the same way but is NOT in the path and must be left
	// alone — otherwise the transform would over-normalize and risk collisions.
	body := []byte(`{"results":[{"dbName":"SALES_V2","userId":"acct-v2"}]}`)
	got, err := normalizer.NormalizeResponse("snowflake", body)
	if err != nil {
		t.Fatalf("NormalizeResponse: %v", err)
	}
	if !strings.Contains(string(got), `"dbName":"SALES"`) {
		t.Fatalf("expected dbName stripped to SALES, got %s", got)
	}
	if !strings.Contains(string(got), `"userId":"acct-v2"`) {
		t.Fatalf("expected userId left untouched, got %s", got)
	}
}
