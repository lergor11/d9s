package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/andreim/d9s/internal/config"
	"github.com/andreim/d9s/internal/ui"
)

var version = "0.1.0-dev"

func main() {
	cfgPath := flag.String("config", config.DefaultPath(), "path to config.yaml")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("d9s", version)
		return
	}

	cfg, warns, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "d9s:", err)
		os.Exit(1)
	}

	if err := ui.Run(cfg, warns, *cfgPath, version); err != nil {
		fmt.Fprintln(os.Stderr, "d9s:", err)
		os.Exit(1)
	}
}
