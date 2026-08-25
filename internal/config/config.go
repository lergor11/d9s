// Package config loads and validates the d9s connection configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// EngineType identifies a supported database engine.
type EngineType string

// Supported engine types, as written in the `type` field of a connection.
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

// clickHouseHTTPPort is the default port of the HTTP interface, which replaces
// the native default when `protocol: http` is selected.
const clickHouseHTTPPort = 8123

// ClickHouse listens for encrypted traffic on ports of its own: the native
// protocol on 9440 and HTTPS on 8443, neither of which is the plaintext port
// plus a convention. A connection that asks for TLS without naming a port
// means the secure one.
const (
	clickHouseNativeTLSPort = 9440
	clickHouseHTTPSPort     = 8443
)

// DefaultConnectTimeout bounds the connect and handshake phase of a connection
// that does not set `connect_timeout`.
const DefaultConnectTimeout = 10 * time.Second

// DefaultResultCap is how many rows one statement loads before a result is
// reported truncated, when the connection does not set `result_cap`.
const DefaultResultCap = 10000

// Protocol selects the wire protocol of a ClickHouse connection.
type Protocol string

// Supported ClickHouse protocols, as written in the `protocol` field.
const (
	// ProtocolNative is the binary protocol on port 9000.
	ProtocolNative Protocol = "native"
	// ProtocolHTTP is the HTTP interface on port 8123, which load balancers
	// and managed offerings usually expose instead.
	ProtocolHTTP Protocol = "http"
)

// Protocols lists the accepted values of the `protocol` field.
var Protocols = []Protocol{ProtocolNative, ProtocolHTTP}

// RedisMode selects the deployment shape of a Redis connection.
type RedisMode string

// Supported Redis deployment modes, as written in the `mode` field.
const (
	// RedisStandalone is a single node addressed directly.
	RedisStandalone RedisMode = "standalone"
	// RedisCluster is an OSS Redis Cluster, seeded from the configured nodes.
	RedisCluster RedisMode = "cluster"
	// RedisSentinel discovers the master through sentinels.
	RedisSentinel RedisMode = "sentinel"
)

// RedisModes lists the accepted values of the `mode` field.
var RedisModes = []RedisMode{RedisStandalone, RedisCluster, RedisSentinel}

// SSH describes an optional bastion hop for a connection.
type SSH struct {
	Bastion     string `yaml:"bastion"`
	User        string `yaml:"user"`
	Port        int    `yaml:"port"`
	AgentSocket string `yaml:"agent_socket"` // optional override
}

// TLSMode selects how much of the server's identity a TLS handshake verifies.
type TLSMode string

// Supported TLS modes, as written in the `mode` field of a `tls` block.
const (
	// TLSDisable connects in plaintext.
	TLSDisable TLSMode = "disable"
	// TLSRequire encrypts but verifies neither the chain nor the hostname.
	TLSRequire TLSMode = "require"
	// TLSVerifyCA validates the certificate chain but not the hostname.
	TLSVerifyCA TLSMode = "verify-ca"
	// TLSVerifyFull validates the chain and the hostname.
	TLSVerifyFull TLSMode = "verify-full"
)

// TLSModes lists the accepted values of a `tls` block's `mode` field.
var TLSModes = []TLSMode{TLSDisable, TLSRequire, TLSVerifyCA, TLSVerifyFull}

// TLS describes the transport encryption of one connection. CA, Cert and Key
// each hold either a filesystem path or a secret reference (`op://...` or
// `${ENV}`) resolved into memory at connect time.
type TLS struct {
	Mode       TLSMode `yaml:"mode"`
	CA         string  `yaml:"ca"`          // PEM roots; empty = system roots
	Cert       string  `yaml:"cert"`        // client certificate, for mutual TLS
	Key        string  `yaml:"key"`         // private key matching Cert
	ServerName string  `yaml:"server_name"` // overrides Host in verify-full
}

// Connection is one configured database endpoint. Host is a hostname or IP
// for every engine, except that postgres also accepts a unix socket directory,
// recognised by its leading slash.
type Connection struct {
	Name     string     `yaml:"name"`
	Type     EngineType `yaml:"type"`
	Host     string     `yaml:"host"`
	Port     int        `yaml:"port"`
	User     string     `yaml:"user"`
	Password string     `yaml:"password"` // op://... or ${ENV} or (discouraged) literal
	Database string     `yaml:"database"` // optional default database
	SSH      *SSH       `yaml:"ssh"`
	TLS      *TLS       `yaml:"tls"`

	// ConnectTimeout bounds connecting and handshaking, for every engine.
	// Written as a duration, e.g. `connect_timeout: 30s`.
	ConnectTimeout time.Duration `yaml:"connect_timeout"`

	// ResultCap is how many rows one statement loads before the result is
	// reported truncated; 0 uses DefaultResultCap.
	ResultCap int `yaml:"result_cap"`

	// Protocol selects the ClickHouse transport; ignored by other engines.
	Protocol Protocol `yaml:"protocol"`

	// Mode, MasterName and Addresses configure a Redis deployment; ignored by
	// other engines. Addresses names the nodes beyond Host:Port, as host:port
	// pairs: further cluster seeds, or further sentinels.
	Mode       RedisMode `yaml:"mode"`
	MasterName string    `yaml:"master_name"`
	Addresses  []string  `yaml:"addresses"`

	// AllowWrite opts the connection in to destructive statements from the MCP
	// server. It is one half of the gate: `d9s mcp` must also be started with
	// --allow-write. The TUI, which has a human at the keyboard, ignores it.
	AllowWrite bool `yaml:"allow_write"`
}

// IsUnixSocket reports whether Host names a unix socket directory rather than
// a network host. Only postgres understands one.
func (c *Connection) IsUnixSocket() bool { return strings.HasPrefix(c.Host, "/") }

// EffectiveTLSMode returns the mode the connection negotiates with: the
// configured one when set, otherwise the default for the connection's shape —
// disable over a unix socket, which never leaves the machine, and behind an
// SSH tunnel, which already encrypts the stream; require for a direct network
// connection, so a managed cloud database works unconfigured.
func (c *Connection) EffectiveTLSMode() TLSMode {
	if c.TLS != nil && c.TLS.Mode != "" {
		return c.TLS.Mode
	}
	if c.SSH != nil || c.IsUnixSocket() {
		return TLSDisable
	}
	return TLSRequire
}

// EffectiveConnectTimeout returns the configured connect timeout, or the
// default when unset.
func (c *Connection) EffectiveConnectTimeout() time.Duration {
	if c.ConnectTimeout > 0 {
		return c.ConnectTimeout
	}
	return DefaultConnectTimeout
}

// EffectiveResultCap returns the configured row cap, or the default when it is
// unset or not a usable count.
func (c *Connection) EffectiveResultCap() int {
	if c.ResultCap > 0 {
		return c.ResultCap
	}
	return DefaultResultCap
}

// EffectiveProtocol returns the ClickHouse transport, defaulting to native.
func (c *Connection) EffectiveProtocol() Protocol {
	if c.Protocol != "" {
		return c.Protocol
	}
	return ProtocolNative
}

// EffectiveRedisMode returns the Redis deployment shape, defaulting to
// standalone.
func (c *Connection) EffectiveRedisMode() RedisMode {
	if c.Mode != "" {
		return c.Mode
	}
	return RedisStandalone
}

// Nodes returns every address of the connection: Host:Port first, then the
// extra Addresses. Redis cluster seeds and sentinels come through here.
func (c *Connection) Nodes() []string {
	nodes := make([]string, 0, 1+len(c.Addresses))
	nodes = append(nodes, net.JoinHostPort(c.Host, strconv.Itoa(c.Port)))
	return append(nodes, c.Addresses...)
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
	return parse(raw, path)
}

// parse decodes and validates config bytes. The writer runs its result through
// here before accepting it, so an edit can never produce a file that Load
// would then reject.
func parse(raw []byte, path string) (*Config, []Warning, error) {
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return &Config{}, nil, nil // an empty file is an empty config
		}
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
			conn.Port = defaultPort(conn)
		}
		if err := checkPort(conn.Name, "port", conn.Port); err != nil {
			return nil, err
		}
		if err := validateEngineOptions(conn); err != nil {
			return nil, err
		}
		if conn.ConnectTimeout < 0 || (conn.ConnectTimeout > 0 && conn.ConnectTimeout < time.Millisecond) {
			return nil, fmt.Errorf("connection %q: connect_timeout %v is not a usable duration (write it like 10s)",
				conn.Name, conn.ConnectTimeout)
		}
		if conn.SSH != nil {
			if conn.SSH.Bastion == "" {
				return nil, fmt.Errorf("connection %q: ssh block missing bastion", conn.Name)
			}
			if conn.IsUnixSocket() {
				// The bastion forwards TCP; a socket only exists locally.
				return nil, fmt.Errorf("connection %q: the unix socket host %s cannot be reached through an ssh bastion",
					conn.Name, conn.Host)
			}
			if conn.SSH.Port == 0 {
				conn.SSH.Port = 22
			}
			if err := checkPort(conn.Name, "ssh port", conn.SSH.Port); err != nil {
				return nil, err
			}
		}
		if err := validateTLS(conn); err != nil {
			return nil, err
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

// validateTLS rejects an unusable tls block. An absent block is valid: the
// mode then comes from Connection.EffectiveTLSMode.
func validateTLS(conn *Connection) error {
	if conn.TLS == nil {
		return nil
	}
	if conn.TLS.Mode != "" && !slices.Contains(TLSModes, conn.TLS.Mode) {
		return fmt.Errorf("connection %q: unknown tls mode %q (want %s)",
			conn.Name, conn.TLS.Mode, join(TLSModes))
	}
	if (conn.TLS.Cert == "") != (conn.TLS.Key == "") {
		return fmt.Errorf("connection %q: tls cert and key must be set together", conn.Name)
	}
	// A unix socket never reaches the network and postgres refuses TLS on one,
	// so the default is disable; asking for anything else cannot be honoured.
	if conn.IsUnixSocket() && conn.TLS.Mode != "" && conn.TLS.Mode != TLSDisable {
		return fmt.Errorf("connection %q: tls mode %q cannot be used with the unix socket host %s",
			conn.Name, conn.TLS.Mode, conn.Host)
	}
	return nil
}

// defaultPort returns the port to use when the connection omits one. It is the
// engine default, except that ClickHouse over HTTP listens elsewhere.
func defaultPort(conn *Connection) int {
	if conn.Type != ClickHouse {
		return defaultPorts[conn.Type]
	}
	secure := conn.EffectiveTLSMode() != TLSDisable
	switch {
	case conn.EffectiveProtocol() == ProtocolHTTP && secure:
		return clickHouseHTTPSPort
	case conn.EffectiveProtocol() == ProtocolHTTP:
		return clickHouseHTTPPort
	case secure:
		return clickHouseNativeTLSPort
	}
	return defaultPorts[ClickHouse]
}

// validateEngineOptions rejects connectivity fields that the connection's
// engine does not understand, and the combinations it cannot satisfy.
func validateEngineOptions(conn *Connection) error {
	if conn.IsUnixSocket() && conn.Type != Postgres {
		return fmt.Errorf("connection %q: only postgres connects over a unix socket, not %s", conn.Name, conn.Type)
	}
	if conn.Protocol != "" {
		if conn.Type != ClickHouse {
			return fmt.Errorf("connection %q: protocol applies to clickhouse connections, not %s", conn.Name, conn.Type)
		}
		if !slices.Contains(Protocols, conn.Protocol) {
			return fmt.Errorf("connection %q: unknown protocol %q (want %s)",
				conn.Name, conn.Protocol, join(Protocols))
		}
	}
	if conn.Type != Redis {
		switch {
		case conn.Mode != "":
			return fmt.Errorf("connection %q: mode applies to redis connections, not %s", conn.Name, conn.Type)
		case conn.MasterName != "":
			return fmt.Errorf("connection %q: master_name applies to redis connections, not %s", conn.Name, conn.Type)
		case len(conn.Addresses) > 0:
			return fmt.Errorf("connection %q: addresses applies to redis connections, not %s", conn.Name, conn.Type)
		}
		return nil
	}
	return validateRedis(conn)
}

// validateRedis checks the deployment-mode fields of a redis connection.
func validateRedis(conn *Connection) error {
	if conn.Mode != "" && !slices.Contains(RedisModes, conn.Mode) {
		return fmt.Errorf("connection %q: unknown mode %q (want %s)", conn.Name, conn.Mode, join(RedisModes))
	}
	mode := conn.EffectiveRedisMode()
	if mode == RedisSentinel && conn.MasterName == "" {
		return fmt.Errorf("connection %q: mode sentinel requires master_name", conn.Name)
	}
	if mode != RedisSentinel && conn.MasterName != "" {
		return fmt.Errorf("connection %q: master_name applies to mode sentinel, not %s", conn.Name, mode)
	}
	if mode == RedisStandalone && len(conn.Addresses) > 0 {
		return fmt.Errorf("connection %q: addresses applies to mode cluster or sentinel, not standalone", conn.Name)
	}
	if mode == RedisCluster && conn.Database != "" && conn.Database != "0" {
		return fmt.Errorf("connection %q: mode cluster has only database 0, not %q", conn.Name, conn.Database)
	}
	for _, addr := range conn.Addresses {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return fmt.Errorf("connection %q: address %q is not host:port: %w", conn.Name, addr, err)
		}
		if host == "" {
			return fmt.Errorf("connection %q: address %q is missing a host", conn.Name, addr)
		}
		n, err := strconv.Atoi(port)
		if err != nil {
			return fmt.Errorf("connection %q: address %q has a non-numeric port", conn.Name, addr)
		}
		if err := checkPort(conn.Name, "address "+addr+" port", n); err != nil {
			return err
		}
	}
	return nil
}

// join renders the accepted values of an enum field for an error message.
func join[T ~string](values []T) string {
	names := make([]string, len(values))
	for i, v := range values {
		names[i] = string(v)
	}
	return strings.Join(names, ", ")
}

// checkPort rejects ports outside 1..65535, which would otherwise be silently
// truncated when handed to the engine drivers as a uint16.
func checkPort(connection, field string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("connection %q: %s %d is out of range (1-65535)", connection, field, port)
	}
	return nil
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
    protocol: http        # native: 9000, or 9440 with tls
                          # http:   8123, or 8443 with tls
    user: default
    password: ${CH_PASSWORD}
    connect_timeout: 30s  # default 10s, applies to every engine
    tls:
      mode: verify-full   # disable | require | verify-ca | verify-full
      ca: /etc/ssl/certs/ch-root.pem
  - name: cache-redis
    type: redis
    host: 10.0.2.1
    mode: cluster         # standalone | cluster | sentinel
    addresses:            # further cluster seeds, or further sentinels
      - 10.0.2.2:6379
      - 10.0.2.3:6379
  - name: local-pg
    type: postgres
    host: /var/run/postgresql   # a leading slash means a unix socket directory
    user: postgres
`
