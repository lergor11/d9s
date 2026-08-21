// Command d9s is a terminal UI for PostgreSQL, ClickHouse, and Redis, with
// SSH bastion support and 1Password-backed secrets.
package main

import (
	"flag"
	"fmt"
	"os"
	"text/template"

	"github.com/andreim/d9s/internal/config"
	"github.com/andreim/d9s/internal/ui"
)

// Build metadata, stamped at link time with -ldflags -X. The defaults are what
// a bare `go build` produces; `make build` substitutes a development string
// carrying the short commit, and GoReleaser substitutes the released tag.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// usageText is printed by --help. The config path is filled in so the user
// sees where their own file belongs, not a generic placeholder.
const usageText = `d9s {{.Version}} — a k9s-style terminal UI for databases.

Usage:
  d9s [flags]

Flags:
  -config path   configuration file (default {{.ConfigPath}})
  -version       print the version and exit
  -help          print this help and exit

Configuration:
  Connections live in a YAML file, by default {{.ConfigPath}}
  (override with -config or the D9S_CONFIG environment variable).
  Starting d9s without one is safe: it shows where the file goes and what
  it should contain.

  Passwords must be references, never literals:
    op://Vault/item/password   read through the 1Password CLI at connect time
    ${ENV_VAR}                 read from the environment

  Add an ssh block to a connection to reach it through a bastion; the key
  comes from the 1Password SSH agent, so no private key is ever read by d9s.

Keys (press ? inside d9s for the bindings of the current view):
  j/k, arrows    move            enter   connect / open
  ctrl+r, F5     run buffer      esc     back one level
  ctrl+x         cancel query    ctrl+j  toggle editor/results focus
  tab            complete the name at the cursor (ctrl+g reloads names)
  ctrl+h         query history   s       schema panel
  e / y          export to file / copy to clipboard
  ? / q          help / quit

Try it against throwaway containers:
  docker run -d --rm --name d9s-pg -e POSTGRES_PASSWORD=secret -p 15432:5432 postgres:16-alpine
  DEMO_PG_PASSWORD=secret d9s -config examples/demo-docker.yaml
`

func usage(out *os.File, configPath string) {
	t := template.Must(template.New("usage").Parse(usageText))
	data := struct{ Version, ConfigPath string }{version, configPath}
	if err := t.Execute(out, data); err != nil {
		fmt.Fprintln(os.Stderr, "d9s: rendering help:", err)
	}
}

func main() {
	defaultConfig := config.DefaultPath()
	cfgPath := flag.String("config", defaultConfig, "path to config.yaml")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = func() { usage(os.Stderr, defaultConfig) }
	flag.Parse()

	if *showVersion {
		fmt.Printf("d9s %s\ncommit: %s\nbuilt:  %s\n", version, commit, date)
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
