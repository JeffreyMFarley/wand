package main

import (
	"fmt"
	"os"

	"wand/proxy"
)

func main() {
	cfg := proxy.Config{}
	router := proxy.NewRouter(cfg)
	if err := router.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
