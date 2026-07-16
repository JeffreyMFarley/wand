package proxy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// IndexEntry represents the semantic metadata for a fixture entry.
type IndexEntry struct {
	Scenario       string   `json:"scenario"`
	Service        string   `json:"service"`
	Captured       string   `json:"captured"`
	CapturedBy     string   `json:"captured_by"`
	Tests          []string `json:"tests"`
	RequestSummary string   `json:"request_summary"`
}

// Store represents a filesystem-backed fixture store.
type Store struct {
	root string
}

func NewStore() *Store {
	return NewStoreWithRoot(".")
}

func NewStoreWithRoot(root string) *Store {
	return &Store{root: root}
}

func (s *Store) Read(service, hash string) (req []byte, resp []byte, err error) {
	reqPath, respPath := s.fixturePaths(service, hash)

	req, err = os.ReadFile(reqPath)
	if err != nil {
		return nil, nil, err
	}
	resp, err = os.ReadFile(respPath)
	if err != nil {
		return nil, nil, err
	}
	return stripHeader(req), stripHeader(resp), nil
}

func (s *Store) Write(service, hash string, req []byte, resp []byte) error {
	baseDir := filepath.Join(s.root, "__fixtures__", service)
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return err
	}

	reqPath := filepath.Join(baseDir, hash+"_req.json")
	respPath := filepath.Join(baseDir, hash+"_resp.json")

	if err := os.WriteFile(reqPath, []byte(formatFixtureHeader(service)+string(req)), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(respPath, []byte(formatFixtureHeader(service)+string(resp)), 0o644); err != nil {
		return err
	}

	index, err := s.LoadIndex()
	if err != nil {
		return err
	}
	index[hash] = IndexEntry{
		Service:        service,
		Captured:       time.Now().Format("2006-01-02"),
		CapturedBy:     "wand/1.0.0",
		RequestSummary: strings.TrimSpace(string(req)),
	}
	return s.WriteIndex(index)
}

func (s *Store) LoadIndex() (map[string]IndexEntry, error) {
	indexPath := filepath.Join(s.root, "__fixtures__", "index.json")
	data, err := os.ReadFile(indexPath)
	if os.IsNotExist(err) {
		return map[string]IndexEntry{}, nil
	}
	if err != nil {
		return nil, err
	}

	var index map[string]IndexEntry
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	if index == nil {
		index = map[string]IndexEntry{}
	}
	return index, nil
}

func (s *Store) WriteIndex(index map[string]IndexEntry) error {
	if err := os.MkdirAll(filepath.Join(s.root, "__fixtures__"), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.root, "__fixtures__", "index.json"), append(data, '\n'), 0o644)
}

func (s *Store) fixturePaths(service, hash string) (string, string) {
	baseDir := filepath.Join(s.root, "__fixtures__", service)
	return filepath.Join(baseDir, hash+"_req.json"), filepath.Join(baseDir, hash+"_resp.json")
}

func formatFixtureHeader(service string) string {
	return fmt.Sprintf("{\"wand_version\":\"1\",\"service\":\"%s\",\"captured\":\"%s\"}\n", service, time.Now().Format("2006-01-02"))
}

func stripHeader(data []byte) []byte {
	text := string(data)
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return data
	}
	if strings.HasPrefix(strings.TrimSpace(lines[0]), "{") {
		if len(lines) > 1 {
			return []byte(strings.Join(lines[1:], "\n"))
		}
		return []byte{}
	}
	return data
}
