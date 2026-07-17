package proxy

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"gopkg.in/yaml.v3"
)

// defaultClaudeModel is used when wand.yaml does not pin a model. Per the
// project's AI-application guidance, this defaults to the latest Opus tier.
const defaultClaudeModel = "claude-haiku-4-5"

// ClaudeClient wraps the Anthropic API for the agentic CLI commands. It is only
// used by developer-facing commands (scaffold/capture/diff/doctor/explain) —
// never in ci mode, which stays fully deterministic and offline.
type ClaudeClient struct {
	api   anthropic.Client
	model anthropic.Model
}

// NewClaudeClient builds a client using ANTHROPIC_API_KEY (or an `ant` auth
// profile) for credentials and the model configured in wand.yaml.
func NewClaudeClient() *ClaudeClient {
	return &ClaudeClient{
		api:   anthropic.NewClient(),
		model: anthropic.Model(loadClaudeModel()),
	}
}

// Model reports the model this client will call, for display in CLI output.
func (c *ClaudeClient) Model() string {
	return string(c.model)
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

// maxCompletionTokens bounds every Complete call. It must be large enough to
// hold a full generated file: scaffold produces whole integration-test files,
// which for a wide service class run well past the 4096 the API defaults toward
// and used to be pinned at here — a too-low cap silently truncated the file
// mid-line, leaving unparsable output on disk.
const maxCompletionTokens = 16384

// Complete sends a single-shot prompt and returns Claude's text response.
// system may be empty. It is deliberately non-streaming: these are short,
// developer-triggered calls, not hot-path requests.
func (c *ClaudeClient) Complete(ctx context.Context, system, user string) (string, error) {
	params := anthropic.MessageNewParams{
		Model:     c.model,
		MaxTokens: maxCompletionTokens,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(user)),
		},
	}
	if system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system}}
	}

	resp, err := c.api.Messages.New(ctx, params)
	if err != nil {
		return "", fmt.Errorf("claude request failed: %w", err)
	}

	var sb strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			sb.WriteString(t.Text)
		}
	}

	// A max_tokens stop means the response was cut off mid-generation. Returning
	// it would write a truncated, unparsable file, so fail loudly instead — the
	// caller can retry or the cap can be raised.
	if resp.StopReason == anthropic.StopReasonMaxTokens {
		return "", fmt.Errorf("claude response truncated at the %d-token limit; output would be incomplete", maxCompletionTokens)
	}

	return strings.TrimSpace(sb.String()), nil
}
