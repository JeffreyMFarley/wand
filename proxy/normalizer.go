package proxy

import "fmt"

// Normalizer applies config-driven normalization to payloads.
type Normalizer struct {
	Config map[string]string
}

func NewNormalizer() *Normalizer {
	return &Normalizer{Config: map[string]string{}}
}

func (n *Normalizer) Normalize(input string) string {
	if input == "" {
		return ""
	}

	return fmt.Sprintf("normalized:%s", input)
}
