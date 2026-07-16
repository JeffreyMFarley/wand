package proxy

import "fmt"

// ClaudeClient is a placeholder for AI integration points.
type ClaudeClient struct{}

func NewClaudeClient() *ClaudeClient {
	return &ClaudeClient{}
}

func (c *ClaudeClient) Prompt(input string) string {
	return fmt.Sprintf("claude:%s", input)
}
