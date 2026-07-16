package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Server handles proxying requests for the configured services.
type Server struct {
	Port       int
	Store      *Store
	Normalizer *Normalizer
	Router     *Router
	cfg        Config
}

func NewServer(cfg Config) *Server {
	port := 8877
	if v := os.Getenv("WAND_PORT"); v != "" {
		fmt.Sscanf(v, "%d", &port)
	}
	return &Server{
		Port:       port,
		Store:      NewStore(),
		Normalizer: NewNormalizer(),
		cfg:        cfg,
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleProxy)
	addr := fmt.Sprintf(":%d", s.Port)
	fmt.Printf("wand proxy listening on %s\n", addr)
	return http.ListenAndServe(addr, mux)
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	service := serviceFromRequest(r)
	if service == "" {
		service = "http"
	}

	body, _ := io.ReadAll(r.Body)
	normalizedReq, err := s.Normalizer.NormalizeRequest(service, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	hash := Hash(normalizedReq)
	mode := strings.ToUpper(os.Getenv("WAND_MODE"))
	if mode == "" {
		mode = "CI"
	}

	switch mode {
	case "CI":
		_, fixtureResp, err := s.Store.Read(service, hash)
		if err != nil {
			http.Error(w, fmt.Sprintf("fixture miss for %s (%s): %s\nnormalized request: %s", service, hash, err, normalizedReq), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixtureResp)
	case "CAPTURE":
		upstream, err := s.resolveUpstream(service)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp, err := s.forward(upstream, r, body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		normalizedResp, err := s.Normalizer.NormalizeResponse(service, resp)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = s.Store.Write(service, hash, normalizedReq, normalizedResp)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	case "PASSTHROUGH":
		upstream, err := s.resolveUpstream(service)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp, err := s.forward(upstream, r, body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		_, _ = w.Write(resp)
	case "LIVE_TEST", " LIVETEST":
		upstream, err := s.resolveUpstream(service)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp, err := s.forward(upstream, r, body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		normalizedResp, err := s.Normalizer.NormalizeResponse(service, resp)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, fixtureResp, readErr := s.Store.Read(service, hash)
		if readErr == nil {
			if !bytes.Equal(normalizedResp, fixtureResp) {
				fmt.Printf("livetest mismatch for %s: live=%s fixture=%s\n", service, normalizedResp, fixtureResp)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	default:
		http.Error(w, fmt.Sprintf("unsupported WAND_MODE %q", mode), http.StatusBadRequest)
	}
}

func (s *Server) resolveUpstream(service string) (string, error) {
	if service == "http" {
		return "http://127.0.0.1:8080", nil
	}
	cfg, err := loadWandConfig()
	if err != nil {
		return "", err
	}
	for _, entry := range cfg.Services {
		if entry.Name == service {
			return entry.UpstreamURL, nil
		}
	}
	return "", fmt.Errorf("no upstream configured for %s", service)
}

type wandConfig struct {
	Services []struct {
		Name        string `yaml:"name"`
		UpstreamURL string `yaml:"upstream_url"`
	} `yaml:"services"`
}

func loadWandConfig() (wandConfig, error) {
	var cfg wandConfig
	data, err := os.ReadFile("wand.yaml")
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (s *Server) forward(upstream string, r *http.Request, body []byte) ([]byte, error) {
	parsed, err := url.Parse(upstream)
	if err != nil {
		return nil, err
	}
	proxyReq, err := http.NewRequest(r.Method, parsed.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	proxyReq.Header = make(http.Header)
	for k, vals := range r.Header {
		proxyReq.Header[k] = append([]string(nil), vals...)
	}
	client := &http.Client{}
	resp, err := client.Do(proxyReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func serviceFromRequest(r *http.Request) string {
	if service := r.Header.Get("X-Wand-Service"); service != "" {
		return service
	}
	if strings.HasPrefix(r.URL.Path, "/") {
		return strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/"), "/")
	}
	return ""
}

func init() {
	_ = httputil.DumpRequestOut
	_ = json.Marshal
}
