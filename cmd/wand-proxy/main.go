package main

import (
	"fmt"
	"os"

	"wand/proxy"
)

func main() {
	router := proxy.NewRouter(proxy.Config{Mode: "proxy"})
	if err := router.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
