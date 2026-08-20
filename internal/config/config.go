// Package config loads and validates the d9s connection configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type EngineType string

const (
	Postgres   EngineType = "postgres"
	ClickHouse EngineType = "clickhouse"
	Redis      EngineType = "redis"
)

var defaultPorts = map[EngineType]int{
	Postgres:   5432,
	ClickHouse: 9000, // native protocol
	Redis:      6379,
}

// SSH describes an optional bastion hop for a connection.
type SSH struct {
	Bastion     string `yaml:"bastion"`
	User        string `yaml:"user"`
	Port        int    `yaml:"port"`
	AgentSocket string `yaml:"agent_socket"` // optional override
}

// Connection is one configured database endpoint.
type Connection struct {
	Name     string     `yaml:"name"`
	Type     EngineType `yaml:"type"`
	Host     string     `yaml:"host"`
	Port     int        `yaml:"port"`
	User     string     `yaml:"user"`
	Password string     `yaml:"password"` // op://... or ${ENV} or (discouraged) literal
	Database string     `yaml:"database"` // optional default database
	SSH      *SSH       `yaml:"ssh"`
}

// Config is the root of config.yaml.
type Config struct {
	Connections []Connection `yaml:"connections"`
}

// Warning is a non-fatal problem worth surfacing in the UI.
type Warning struct {
	Connection string
	Message    string
}

// DefaultPath returns ~/.config/d9s/config.yaml, honoring D9S_CONFIG.
func DefaultPath() string {
	if p := os.Getenv("D9S_CONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(home, ".config", "d9s", "config.yaml")
}

// IsOpRef reports whether v is a 1Password secret reference.
func IsOpRef(v string) bool { return strings.HasPrefix(v, "op://") }

// IsEnvRef reports whether v is a ${ENV_VAR} reference.
func IsEnvRef(v string) bool {
	return strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") && len(v) > 3
}

// Load reads and validates the config file. A missing file is not an error:
// it returns an empty config so the UI can show onboarding help.
func Load(path string) (*Config, []Warning, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil, nil
		}
		return nil, nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	warns, err := cfg.validate()
	if err != nil {
		return nil, nil, fmt.Errorf("config %s: %w", path, err)
	}
	return &cfg, warns, nil
}

func (c *Config) validate() ([]Warning, error) {
	var warns []Warning
	seen := map[string]bool{}
	for i := range c.Connections {
		conn := &c.Connections[i]
		if conn.Name == "" {
			return nil, fmt.Errorf("connection #%d: missing name", i+1)
		}
		if seen[conn.Name] {
			return nil, fmt.Errorf("duplicate connection name %q", conn.Name)
		}
		seen[conn.Name] = true
		if _, ok := defaultPorts[conn.Type]; !ok {
			return nil, fmt.Errorf("connection %q: unknown type %q (want postgres, clickhouse, or redis)", conn.Name, conn.Type)
		}
		if conn.Host == "" {
			return nil, fmt.Errorf("connection %q: missing host", conn.Name)
		}
		if conn.Port == 0 {
			conn.Port = defaultPorts[conn.Type]
		}
		if conn.SSH != nil {
			if conn.SSH.Bastion == "" {
				return nil, fmt.Errorf("connection %q: ssh block missing bastion", conn.Name)
			}
			if conn.SSH.Port == 0 {
				conn.SSH.Port = 22
			}
		}
		if conn.Password != "" && !IsOpRef(conn.Password) && !IsEnvRef(conn.Password) {
			warns = append(warns, Warning{
				Connection: conn.Name,
				Message:    "password is stored in plaintext; prefer an op://vault/item/field reference",
			})
		}
	}
	return warns, nil
}

// Sample is shown when no config file exists yet.
const Sample = `connections:
  - name: prod-pg
    type: postgres
    host: 10.0.1.5
    port: 5432
    user: app
    password: op://Infra/prod-pg/password
    ssh:
      bastion: bastion.corp.com
      user: deploy
  - name: analytics-ch
    type: clickhouse
    host: ch.internal
    user: default
    password: ${CH_PASSWORD}
  - name: cache-redis
    type: redis
    host: 127.0.0.1
`
