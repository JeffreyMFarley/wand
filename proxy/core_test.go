package proxy

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestHashProducesHyphenatedBlake2b(t *testing.T) {
	hash := Hash([]byte("hello"))
	if matched, err := regexp.MatchString(`^[0-9a-f]{4}(?:-[0-9a-f]{4}){15}$`, hash); err != nil || !matched {
		t.Fatalf("expected hyphenated 32-char hash, got %q", hash)
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
