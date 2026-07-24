package proxy

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// This file implements the on-disk verdict cache for `wand scan`. It stores the
// one expensive part of a classification — the LLM qualify verdict — keyed by
// file content hash, as an append-only JSONL log. Appending one line the moment
// each verdict is known (rather than at the end) means an interrupted scan loses
// nothing: a re-run replays the log and only re-classifies files whose content
// changed or were never reached. It mirrors the store's access.jsonl technique.
//
// dest/exists are intentionally NOT cached — they are a cheap stat recomputed
// every run, so adding or moving a test file is always reflected.

// scanCacheFile is the log's path, relative to the working directory. It lives
// under .wand/ (not __fixtures__/, which is captured API responses) since a
// source-scan cache is a distinct, disposable concern. Add it to .gitignore.
const scanCacheFile = ".wand/scan-cache.jsonl"

// scanVerdict is one cached classification line. Version salts the entry with
// the prompt+model that produced it; a line whose version no longer matches is
// ignored on load, so editing the qualify prompt or switching models silently
// invalidates stale verdicts instead of trusting them.
type scanVerdict struct {
	Hash      string `json:"h"`
	Version   string `json:"v"`
	Qualifies bool   `json:"q"`
	Reason    string `json:"r,omitempty"`
}

// scanCache is a concurrency-safe, append-only verdict log. get reads from the
// in-memory map replayed at open; put records a verdict both in memory and as a
// durable JSONL line. Safe for the concurrent classifiers in scanSources.
type scanCache struct {
	path    string
	version string
	mu      sync.Mutex
	m       map[string]scanVerdict
	f       *os.File // append handle, opened lazily on first put
	hits    int
}

// scanCacheVersion derives the salt that scopes cached verdicts to the exact
// prompt and model that produced them.
func scanCacheVersion(model string) string {
	return Hash([]byte(scaffoldQualifySystemPrompt + "\x00" + model))
}

// openScanCache loads any existing log at path, keeping only entries matching
// version (stale-version and superseded lines are dropped, last write winning).
// A missing or unreadable log yields an empty cache rather than an error: the
// cache is an optimization, never a correctness dependency, so scanning must
// proceed even if it can't be read.
func openScanCache(path, version string) *scanCache {
	c := &scanCache{path: path, version: version, m: map[string]scanVerdict{}}

	f, err := os.Open(path)
	if err != nil {
		return c
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var v scanVerdict
		if json.Unmarshal(line, &v) != nil {
			continue // tolerate a torn final line from an interrupted write
		}
		if v.Version == version {
			c.m[v.Hash] = v // last line for a hash wins
		}
	}
	return c
}

// get returns the cached verdict for a content hash, if one was recorded under
// the current version. A hit is counted for the run's summary.
func (c *scanCache) get(hash string) (scanVerdict, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[hash]
	if ok {
		c.hits++
	}
	return v, ok
}

// put records a verdict in memory and appends it as one durable JSONL line. A
// write error is swallowed: a scan that can't persist its cache should still
// finish and report, just without speeding up the next run. The append handle
// is opened lazily so a scan that is entirely cache hits never creates the file.
func (c *scanCache) put(hash string, qualifies bool, reason string) {
	v := scanVerdict{Hash: hash, Version: c.version, Qualifies: qualifies, Reason: reason}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[hash] = v

	if c.f == nil {
		if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
			return
		}
		f, err := os.OpenFile(c.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		c.f = f
	}
	line, err := json.Marshal(v)
	if err != nil {
		return
	}
	c.f.Write(append(line, '\n'))
}

// Close releases the append handle. The bytes are already durable — each put
// writes them straight through — so Close is only cleanup, safe to call even if
// no line was ever written.
func (c *scanCache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.f == nil {
		return nil
	}
	err := c.f.Close()
	c.f = nil
	return err
}
