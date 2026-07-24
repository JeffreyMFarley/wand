package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"gopkg.in/yaml.v3"
)

// defaultClaudeModel is used when wand.yaml does not pin a model.
const defaultClaudeModel = "claude-haiku-4-5"

// ClaudeClient routes agentic CLI commands through the local `claude` binary,
// inheriting whatever account it is authenticated with. This means no API key
// is required — billing follows the `claude auth status` session, which for
// FullStack developers is the company org.
//
// It is only used by developer-facing commands (scaffold/capture/diff/doctor/
// explain) — never in ci mode, which stays fully deterministic and offline.
type ClaudeClient struct {
	model string
}

// NewClaudeClient builds a client using the model configured in wand.yaml,
// falling back to defaultClaudeModel. No credentials are resolved here —
// the claude subprocess handles auth at call time.
func NewClaudeClient() *ClaudeClient {
	return &ClaudeClient{
		model: loadClaudeModel(),
	}
}

// Model reports the model this client will use, for display in CLI output.
func (c *ClaudeClient) Model() string {
	return c.model
}

func loadClaudeModel() string {
	data, err := os.ReadFile("wand.yaml")
	if err == nil {
		var cfg struct {
			Claude struct {
				Model string `yaml:"model"`
			} `yaml:"claude"`
		}
		if yaml.Unmarshal(data, &cfg) == nil && cfg.Claude.Model != "" {
			return cfg.Claude.Model
		}
	}
	return defaultClaudeModel
}

// Usage reports the tokens one Complete call consumed. Only the non-cached
// input and output tokens are tracked — the new work the call did — which is
// what the scan progress meter accumulates; the repeated cached system prompt
// is not new work and is deliberately excluded.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Tokens is the total non-cached tokens the call processed (input + output).
func (u Usage) Tokens() int { return u.InputTokens + u.OutputTokens }

// Complete sends a single-shot prompt to the `claude` CLI and returns the text
// response, discarding token usage. It wraps CompleteWithUsage for the callers
// (scaffold/capture/doctor/explain) that don't display usage.
func (c *ClaudeClient) Complete(ctx context.Context, system, user string) (string, error) {
	text, _, err := c.CompleteWithUsage(ctx, system, user)
	return text, err
}

// CompleteWithUsage sends a single-shot prompt to the `claude` CLI and returns
// the text response plus the call's token usage. system may be empty — when
// provided it is prepended to the user message with a blank line separator,
// which is how claude -p receives a system prompt.
//
// It requests --output-format json so usage rides back with the result; the
// result field carries the same text --output-format text would have returned.
// The call is deliberately non-streaming and synchronous: these are short,
// developer-triggered operations, not hot-path requests.
func (c *ClaudeClient) CompleteWithUsage(ctx context.Context, system, user string) (string, Usage, error) {
	prompt := user
	if system != "" {
		prompt = system + "\n\n" + user
	}

	cmd := exec.CommandContext(ctx, "claude",
		"--print",
		"--model", c.model,
		"--output-format", "json",
		prompt,
	)

	out, err := cmd.Output()
	if err != nil {
		// exec.ExitError carries stderr, which has the claude error message.
		if ee, ok := err.(*exec.ExitError); ok {
			return "", Usage{}, fmt.Errorf("claude request failed: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", Usage{}, fmt.Errorf("claude request failed: %w", err)
	}

	var payload struct {
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
		Usage   struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return "", Usage{}, fmt.Errorf("claude returned unparseable JSON: %w", err)
	}
	if payload.IsError {
		// A structured failure (is_error) reports the message in result.
		return "", Usage{}, fmt.Errorf("claude request failed: %s", strings.TrimSpace(payload.Result))
	}

	usage := Usage{InputTokens: payload.Usage.InputTokens, OutputTokens: payload.Usage.OutputTokens}
	return strings.TrimSpace(payload.Result), usage, nil
}
