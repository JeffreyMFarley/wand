package proxy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ServiceConfig describes the normalization rules for a service.
type ServiceConfig struct {
	Name          string `yaml:"name"`
	Version       string `yaml:"version"`
	Normalization struct {
		Request  RuleSet `yaml:"request"`
		Response RuleSet `yaml:"response"`
	} `yaml:"normalization"`
}

// RuleSet contains normalized transformations for one pass.
type RuleSet struct {
	RemoveFields []string   `yaml:"remove_fields"`
	Sentinels    []Sentinel `yaml:"sentinels"`
	Patterns     []Pattern  `yaml:"patterns"`
}

// Sentinel replaces a field value with a known constant.
type Sentinel struct {
	Field string `yaml:"field"`
	Value string `yaml:"value"`
}

// Pattern applies a built-in transformation to a JSON payload.
type Pattern struct {
	Description string `yaml:"description"`
	Location    string `yaml:"location"`
	Transform   string `yaml:"transform"`
}

// Normalizer applies config-driven normalization to payloads.
type Normalizer struct{}

func NewNormalizer() *Normalizer {
	return &Normalizer{}
}

func (n *Normalizer) NormalizeRequest(service string, body []byte) ([]byte, error) {
	return n.normalize(service, body, "request")
}

func (n *Normalizer) NormalizeResponse(service string, body []byte) ([]byte, error) {
	return n.normalize(service, body, "response")
}

func (n *Normalizer) normalize(service string, body []byte, section string) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}
	config, err := n.loadServiceConfig(service)
	if err != nil {
		return nil, err
	}

	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body, nil
	}

	var rules RuleSet
	if section == "request" {
		rules = config.Normalization.Request
	} else {
		rules = config.Normalization.Response
	}

	if object, ok := payload.(map[string]any); ok {
		for _, field := range rules.RemoveFields {
			delete(object, field)
		}
		for _, sentinel := range rules.Sentinels {
			if _, ok := object[sentinel.Field]; ok {
				object[sentinel.Field] = sentinel.Value
			}
		}
	}

	payload = applyPatterns(payload, rules.Patterns)
	return json.Marshal(payload)
}

func (n *Normalizer) loadServiceConfig(service string) (ServiceConfig, error) {
	var cfg ServiceConfig
	paths := []string{
		filepath.Join("services", service+".yaml"),
		filepath.Join(".", "services", service+".yaml"),
	}
	for _, dir := range candidateDirs() {
		paths = append(paths, filepath.Join(dir, "services", service+".yaml"))
	}
	for _, candidate := range paths {
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, err
		}
		return cfg, nil
	}
	return cfg, fmt.Errorf("service config for %s not found", service)
}

func candidateDirs() []string {
	wd, _ := os.Getwd()
	parts := strings.Split(wd, string(os.PathSeparator))
	results := make([]string, 0, len(parts))
	for i := 1; i <= len(parts); i++ {
		prefix := strings.Join(parts[:i], string(os.PathSeparator))
		if prefix == "" {
			continue
		}
		results = append(results, prefix)
	}
	return results
}

func applyPatterns(payload any, patterns []Pattern) any {
	for _, pattern := range patterns {
		switch pattern.Transform {
		case "strip_version_suffix":
			payload = transformValue(payload, stripVersionSuffix)
		case "normalize_timestamps":
			payload = transformValue(payload, normalizeTimestamps)
		}
	}
	return payload
}

func transformValue(value any, fn func(string) string) any {
	switch v := value.(type) {
	case string:
		return fn(v)
	case []any:
		for i := range v {
			v[i] = transformValue(v[i], fn)
		}
		return v
	case map[string]any:
		for key, child := range v {
			v[key] = transformValue(child, fn)
		}
		return v
	default:
		return value
	}
}

func stripVersionSuffix(input string) string {
	var re = regexp.MustCompile(`(?i)(?:[._-])v\d+$`)
	return re.ReplaceAllString(input, "")
}

func normalizeTimestamps(input string) string {
	var ts = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})?`)
	if strings.Contains(strings.ToUpper(input), "CURRENT_TIMESTAMP") || strings.Contains(strings.ToUpper(input), "NOW(") {
		return "1970-01-01T00:00:00Z"
	}
	if ts.MatchString(input) {
		return "1970-01-01T00:00:00Z"
	}
	return input
}
