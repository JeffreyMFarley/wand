package proxy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config holds basic routing settings for the CLI entry points.
type Config struct {
	Mode    string
	Service string
}

// Router dispatches CLI commands to the appropriate workflow.
type Router struct {
	cfg Config
}

func NewRouter(cfg Config) *Router {
	return &Router{cfg: cfg}
}

func (r *Router) Run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: wand <command> [args]")
	}

	command := strings.ToLower(args[0])
	remaining := args[1:]

	switch command {
	case "init":
		return r.runInit(remaining)
	case "proxy":
		return r.runProxy(remaining)
	case "capture":
		return r.runCapture(remaining)
	case "diff":
		return r.runDiff(remaining)
	case "doctor":
		return r.runDoctor(remaining)
	case "explain":
		return r.runExplain(remaining)
	case "scaffold":
		return r.runScaffold(remaining)
	case "verify":
		return r.runVerify(remaining)
	case "normalizer":
		return r.runNormalizer(remaining)
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func (r *Router) runInit(args []string) error {
	_ = args
	configPath := filepath.Join(".", "wand.yaml")
	content := "name: wand\nmode: proxy\n"
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		return err
	}
	fmt.Println("created wand.yaml")
	return nil
}

func (r *Router) runProxy(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: wand proxy <start|stop>")
	}

	subcommand := strings.ToLower(args[0])
	switch subcommand {
	case "start":
		if os.Getenv("WAND_NONBLOCKING") != "" {
			fmt.Printf("starting wand proxy on port %d (non-blocking)\n", 8877)
			return nil
		}
		server := NewServer(r.cfg)
		fmt.Printf("starting wand proxy on port %d\n", server.Port)
		return server.Start()
	case "stop":
		fmt.Println("proxy sidecar placeholder: stopped")
	default:
		return fmt.Errorf("unknown proxy command %q", subcommand)
	}
	return nil
}

func (r *Router) runCapture(args []string) error {
	if len(args) == 0 {
		fmt.Println("capture placeholder: no scope provided")
		return nil
	}
	if len(args) >= 2 && args[0] == "--tests" {
		fmt.Printf("capture placeholder: explicit scope %s\n", args[1])
		return nil
	}
	fmt.Printf("capture placeholder: %s\n", strings.Join(args, " "))
	return nil
}

func (r *Router) runDiff(args []string) error {
	if len(args) >= 2 && args[0] == "--pr" {
		fmt.Printf("diff placeholder: PR %s\n", args[1])
		return nil
	}
	fmt.Println("diff placeholder: semantic diff of changed fixtures")
	return nil
}

func (r *Router) runDoctor(args []string) error {
	_ = args
	fmt.Println("doctor placeholder: live test and divergence classification")
	return nil
}

func (r *Router) runExplain(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: wand explain <hash>")
	}
	fmt.Printf("explain placeholder: %s\n", args[0])
	return nil
}

func (r *Router) runScaffold(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: wand scaffold <description>")
	}
	fmt.Printf("scaffold placeholder: %s\n", strings.Join(args, " "))
	return nil
}

func (r *Router) runVerify(args []string) error {
	_ = args
	fmt.Println("verify placeholder: CI mode report")
	return nil
}

func (r *Router) runNormalizer(args []string) error {
	fmt.Println("normalizer mode placeholder")
	_ = args
	return nil
}
