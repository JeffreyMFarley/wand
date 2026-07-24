package proxy

import (
	"bytes"
	"path/filepath"
	"strings"
)

// This file implements the cheap static pre-filter that runs before the qualify
// LLM call during `wand scan`. Its sole guarantee is soundness: it must never
// reject a file the model would qualify. A false positive (a file it passes to
// the model that turns out not to qualify) merely costs one API call; a false
// negative would silently hide a real test subject, so the signal lists below
// are deliberately a generous superset of the qualify prompt's triggers.

// godFunctionLines mirrors the qualify prompt's "god function" rule: a single
// function exceeding this many lines qualifies a file on length alone, with no
// external call. A file with no external-call signal can therefore still be a
// test subject — but only if it is at least this long, so the pre-filter only
// rejects signal-free files shorter than this.
const godFunctionLines = 400

// prefilterReason is the skip reason recorded for a file the pre-filter rejects
// without an API call.
const prefilterReason = "no external-call signals"

// externalSignals maps a source-file extension to lowercase substrings whose
// presence might indicate a direct external call, keyed off the qualify prompt's
// per-language checklist. Kept generous on purpose (see file comment): matching
// too much is safe, matching too little is not.
var externalSignals = map[string][]string{
	".py": {"boto3", "botocore", "requests", "httpx", "aiohttp", "opensearch",
		"psycopg", "sqlalchemy", "redis", "pymongo", "http", "urllib", "socket",
		"grpc", "kafka", "elasticsearch", "snowflake", "graphql"},
	".go": {"net/http", "database/sql", "aws-sdk", "grpc", "redis", "mongo",
		"elastic", "http.", "sql.", "kafka", "snowflake", "graphql"},
	".rb": {"aws-sdk", "faraday", "httparty", "net/http", "activerecord",
		"redis", "mongo", "elasticsearch", "http", "grpc", "graphql"},
	".php": {"db::", "http::", "eloquent", "redis", "storage::", "guzzlehttp",
		"aws\\", "s3client", "curl", "http", "graphql"},
	".java": {"httpclient", "httpurlconnection", "java.sql", "datasource",
		"drivermanager", "software.amazon.awssdk", "com.amazonaws", "okhttp",
		"resttemplate", "webclient", "entitymanager", "sessionfactory", "jedis",
		"lettuce", "mongoclient", "elasticsearch", "opensearch", "http", "grpc"},
}

// jsSignals is shared by every JS-family extension (.js/.jsx/.ts/.tsx), which
// classify identically.
var jsSignals = []string{"fetch", "axios", "http", "https", "aws-sdk", "@aws-sdk",
	"mysql", "mongoose", "mongodb", "redis", "opensearch", "graphql", "grpc",
	"elasticsearch", "socket", "snowflake"}

func init() {
	for _, ext := range []string{".js", ".jsx", ".ts", ".tsx"} {
		externalSignals[ext] = jsSignals
	}
}

// mayMakeExternalCall is the pre-filter: false means the file provably cannot
// qualify, so the caller may skip the API call. It returns true whenever a
// language signal token appears OR the file is long enough to possibly contain
// a god function (see godFunctionLines) — and always for an unrecognized
// extension, deferring that case to the model. This is sound by construction:
// the only ways a file can qualify are a signal token or the god-function rule,
// and it never rules out either without proof.
func mayMakeExternalCall(src string, data []byte) bool {
	signals, known := externalSignals[strings.ToLower(filepath.Ext(src))]
	if !known {
		return true
	}
	lc := bytes.ToLower(data)
	for _, s := range signals {
		if bytes.Contains(lc, []byte(s)) {
			return true
		}
	}
	// No signal token. Only the god-function rule could still qualify the file,
	// and that needs one function longer than godFunctionLines — impossible if
	// the whole file has fewer non-blank lines. Counting blank lines out but
	// comments in overcounts, which only ever errs toward asking the model.
	return nonBlankLineCount(data) > godFunctionLines
}

// nonBlankLineCount counts lines containing any non-whitespace byte. It is a
// deliberate overcount of a function's real body (comments included, no
// per-function scoping) so the god-function short-circuit never drops a file
// that could hold a >400-line function.
func nonBlankLineCount(data []byte) int {
	n := 0
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) > 0 {
			n++
		}
	}
	return n
}
