package proxy

import (
	"context"
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

// Complete sends a single-shot prompt to the `claude` CLI and returns the
// text response. system may be empty — when provided it is prepended to the
// user message with a blank line separator, which is how claude -p receives
// a system prompt.
//
// The call is deliberately non-streaming and synchronous: these are short,
// developer-triggered operations, not hot-path requests.
func (c *ClaudeClient) Complete(ctx context.Context, system, user string) (string, error) {
	prompt := user
	if system != "" {
		prompt = system + "\n\n" + user
	}

	cmd := exec.CommandContext(ctx, "claude",
		"--print",
		"--model", c.model,
		"--output-format", "text",
		prompt,
	)

	out, err := cmd.Output()
	if err != nil {
		// exec.ExitError carries stderr, which has the claude error message.
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("claude request failed: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("claude request failed: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}
