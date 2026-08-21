package db

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lergor11/d9s/internal/config"
)

// mapResolver is a stand-in for *secrets.Resolver holding literal values.
type mapResolver map[string]string

func (m mapResolver) Resolve(_ context.Context, ref string) (string, error) {
	v, ok := m[ref]
	if !ok {
		return "", fmt.Errorf("no such secret %q", ref)
	}
	return v, nil
}

// authority is a throwaway CA that issues the certificates used below. Nothing
// here touches the filesystem unless a test writes a PEM to its own TempDir.
type authority struct {
	pem  []byte
	cert *x509.Certificate
	key  ed25519.PrivateKey
}

func newAuthority(t *testing.T) *authority {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(t),
		Subject:               pkix.Name{CommonName: "d9s test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, key)
	if err != nil {
		t.Fatalf("creating CA certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing CA certificate: %v", err)
	}
	return &authority{
		pem:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		cert: cert,
		key:  key,
	}
}

// issue returns a PEM certificate and key signed by the authority.
func (a *authority) issue(t *testing.T, commonName string, dnsNames ...string) (certPEM, keyPEM []byte) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(t),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:     dnsNames,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, pub, a.key)
	if err != nil {
		t.Fatalf("creating leaf certificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshalling leaf key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

func serial(t *testing.T) *big.Int {
	t.Helper()
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 96))
	if err != nil {
		t.Fatalf("generating serial: %v", err)
	}
	return n
}

// writePEM saves material to the test's temporary directory and returns its
// path, standing in for a certificate the user keeps on disk.
func writePEM(t *testing.T, name string, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return path
}

func TestTLSConfigForBuildsOneConfigPerMode(t *testing.T) {
	ca := newAuthority(t)
	caPath := writePEM(t, "ca.pem", ca.pem)

	tests := []struct {
		name           string
		conn           config.Connection
		wantNil        bool
		wantSkipVerify bool
		wantVerifyFunc bool
		wantRoots      bool
		wantServerName string
	}{
		{
			name:    "no tls block behind a tunnel disables tls",
			conn:    config.Connection{Name: "tunneled", Host: "db.internal", SSH: &config.SSH{Bastion: "b"}},
			wantNil: true,
		},
		{
			name:           "no tls block on a direct connection requires tls",
			conn:           config.Connection{Name: "cloud", Host: "db.neon.tech"},
			wantSkipVerify: true,
		},
		{
			name:    "explicit disable",
			conn:    config.Connection{Name: "plain", Host: "h", TLS: &config.TLS{Mode: config.TLSDisable}},
			wantNil: true,
		},
		{
			// A socket never reaches the network and postgres refuses TLS on
			// one, so require-by-default must not apply to it.
			name:    "no tls block over a unix socket disables tls",
			conn:    config.Connection{Name: "socket", Type: config.Postgres, Host: "/var/run/postgresql"},
			wantNil: true,
		},
		{
			name:           "require does not verify",
			conn:           config.Connection{Name: "req", Host: "h", TLS: &config.TLS{Mode: config.TLSRequire, CA: caPath}},
			wantSkipVerify: true,
		},
		{
			name:           "verify-ca checks the chain but not the name",
			conn:           config.Connection{Name: "vca", Host: "h", TLS: &config.TLS{Mode: config.TLSVerifyCA, CA: caPath}},
			wantSkipVerify: true,
			wantVerifyFunc: true,
		},
		{
			name:           "verify-full checks the host name",
			conn:           config.Connection{Name: "vfull", Host: "db.neon.tech", TLS: &config.TLS{Mode: config.TLSVerifyFull, CA: caPath}},
			wantRoots:      true,
			wantServerName: "db.neon.tech",
		},
		{
			name:           "verify-full honours server_name",
			conn:           config.Connection{Name: "vfull-sni", Host: "10.0.0.1", TLS: &config.TLS{Mode: config.TLSVerifyFull, ServerName: "db.neon.tech"}},
			wantServerName: "db.neon.tech",
		},
		{
			name:           "verify-full without a ca falls back to the system roots",
			conn:           config.Connection{Name: "vfull-sys", Host: "db.neon.tech", TLS: &config.TLS{Mode: config.TLSVerifyFull}},
			wantServerName: "db.neon.tech",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tlsConfigFor(context.Background(), Target{Config: tt.conn})
			if err != nil {
				t.Fatalf("tlsConfigFor: %v", err)
			}
			if tt.wantNil {
				if got != nil {
					t.Fatalf("got a tls.Config, want nil for plaintext")
				}
				return
			}
			if got == nil {
				t.Fatal("got nil, want a tls.Config")
			}
			if got.MinVersion < tls.VersionTLS12 {
				t.Errorf("MinVersion = %#x, want at least TLS 1.2", got.MinVersion)
			}
			if got.InsecureSkipVerify != tt.wantSkipVerify {
				t.Errorf("InsecureSkipVerify = %v, want %v", got.InsecureSkipVerify, tt.wantSkipVerify)
			}
			if (got.VerifyPeerCertificate != nil) != tt.wantVerifyFunc {
				t.Errorf("VerifyPeerCertificate set = %v, want %v", got.VerifyPeerCertificate != nil, tt.wantVerifyFunc)
			}
			if (got.RootCAs != nil) != tt.wantRoots {
				t.Errorf("RootCAs set = %v, want %v", got.RootCAs != nil, tt.wantRoots)
			}
			if got.ServerName != tt.wantServerName {
				t.Errorf("ServerName = %q, want %q", got.ServerName, tt.wantServerName)
			}
		})
	}
}

func TestTLSConfigForNeverSkipsVerificationOutsideRequire(t *testing.T) {
	ca := newAuthority(t)
	caPath := writePEM(t, "ca.pem", ca.pem)

	for _, mode := range []config.TLSMode{config.TLSVerifyCA, config.TLSVerifyFull} {
		t.Run(string(mode), func(t *testing.T) {
			cfg, err := tlsConfigFor(context.Background(), Target{Config: config.Connection{
				Name: "c", Host: "h", TLS: &config.TLS{Mode: mode, CA: caPath},
			}})
			if err != nil {
				t.Fatalf("tlsConfigFor: %v", err)
			}
			// require is the only mode allowed to accept any certificate, so
			// every other mode must run a real check somewhere.
			if cfg.InsecureSkipVerify && cfg.VerifyPeerCertificate == nil {
				t.Error("verification is skipped with no replacement check")
			}
		})
	}
}

func TestTLSConfigForRejectsBadMaterial(t *testing.T) {
	ca := newAuthority(t)
	certPEM, _ := ca.issue(t, "client")
	_, unrelatedKeyPEM := ca.issue(t, "someone else")
	missing := filepath.Join(t.TempDir(), "absent.pem")
	notPEM := writePEM(t, "junk.pem", []byte("this is not a certificate"))

	tests := []struct {
		name    string
		tlsCfg  config.TLS
		secrets SecretResolver
		want    string
	}{
		{
			name:   "unknown mode",
			tlsCfg: config.TLS{Mode: "yes-please"},
			want:   "unknown tls mode",
		},
		{
			name:   "missing ca file",
			tlsCfg: config.TLS{Mode: config.TLSVerifyFull, CA: missing},
			want:   "tls ca:",
		},
		{
			name:   "ca file without a certificate",
			tlsCfg: config.TLS{Mode: config.TLSVerifyFull, CA: notPEM},
			want:   "no PEM certificate found",
		},
		{
			name:   "missing client certificate file",
			tlsCfg: config.TLS{Mode: config.TLSRequire, Cert: missing, Key: missing},
			want:   "tls cert:",
		},
		{
			name:   "key belonging to another certificate",
			tlsCfg: config.TLS{Mode: config.TLSRequire, Cert: writePEM(t, "c.pem", certPEM), Key: writePEM(t, "k.pem", unrelatedKeyPEM)},
			want:   "tls client keypair:",
		},
		{
			name:   "op reference without a resolver",
			tlsCfg: config.TLS{Mode: config.TLSRequire, Cert: "op://Infra/db/cert", Key: "op://Infra/db/key"},
			want:   "no secret resolver available",
		},
		{
			name:    "op reference the resolver cannot find",
			tlsCfg:  config.TLS{Mode: config.TLSRequire, Cert: "op://Infra/db/cert", Key: "op://Infra/db/key"},
			secrets: mapResolver{},
			want:    "no such secret",
		},
		{
			name:    "op reference resolving to nothing",
			tlsCfg:  config.TLS{Mode: config.TLSRequire, Cert: "op://Infra/db/cert", Key: "op://Infra/db/key"},
			secrets: mapResolver{"op://Infra/db/cert": string(certPEM), "op://Infra/db/key": ""},
			want:    "resolved to an empty value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tlsConfigFor(context.Background(), Target{
				Config:  config.Connection{Name: "c", Host: "h", TLS: &tt.tlsCfg},
				Secrets: tt.secrets,
			})
			if err == nil {
				t.Fatalf("tlsConfigFor succeeded, want an error mentioning %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestTLSConfigForLoadsClientCertificateFrom1Password(t *testing.T) {
	ca := newAuthority(t)
	certPEM, keyPEM := ca.issue(t, "client")
	dir := t.TempDir()

	cfg, err := tlsConfigFor(context.Background(), Target{
		Config: config.Connection{Name: "mtls", Host: "h", TLS: &config.TLS{
			Mode: config.TLSVerifyFull,
			Cert: "op://Infra/db/cert",
			Key:  "op://Infra/db/key",
		}},
		Secrets: mapResolver{
			"op://Infra/db/cert": string(certPEM),
			"op://Infra/db/key":  string(keyPEM),
		},
	})
	if err != nil {
		t.Fatalf("tlsConfigFor: %v", err)
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("got %d client certificates, want 1", len(cfg.Certificates))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading temp dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("certificate material was written to disk: %v", entries)
	}
}

// greeting is what tlsServer sends once a connection is fully established.
// Reading it proves the session survived the handshake, which matters under
// TLS 1.3 where the client finishes before the server checks its certificate.
const greeting = "ok"

// tlsServer starts a TLS listener that greets every accepted connection.
func tlsServer(t *testing.T, cert tls.Certificate, clientCAs *x509.CertPool) net.Listener {
	t.Helper()
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	if clientCAs != nil {
		cfg.ClientCAs = clientCAs
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			if err := conn.(*tls.Conn).HandshakeContext(context.Background()); err == nil {
				_, _ = conn.Write([]byte(greeting))
			}
			_ = conn.Close()
		}
	}()
	return ln
}

// handshake dials the listener with the target's TLS settings, exercising the
// same wrapper the tunneled adapters use, and reads the server's greeting.
func handshake(t *testing.T, ln net.Listener, target Target) error {
	t.Helper()
	host, portRaw, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("splitting listener address: %v", err)
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil {
		t.Fatalf("listener port %q: %v", portRaw, err)
	}
	if target.Config.Host == "" {
		target.Config.Host = host
	}
	target.Config.Port = port

	cfg, err := tlsConfigFor(context.Background(), target)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	conn, err := tlsDialer(dial, cfg)(ctx, "tcp", ln.Addr().String())
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("setting the read deadline: %v", err)
	}
	buf := make([]byte, len(greeting))
	if _, err := io.ReadFull(conn, buf); err != nil {
		return fmt.Errorf("reading the server greeting: %w", err)
	}
	return nil
}

func TestTLSHandshakeEnforcesEachMode(t *testing.T) {
	ca := newAuthority(t)
	caPath := writePEM(t, "ca.pem", ca.pem)
	serverCert, serverKey := ca.issue(t, "d9s test server", "localhost")
	pair, err := tls.X509KeyPair(serverCert, serverKey)
	if err != nil {
		t.Fatalf("server keypair: %v", err)
	}
	other := newAuthority(t)
	otherCAPath := writePEM(t, "other-ca.pem", other.pem)

	ln := tlsServer(t, pair, nil)

	tests := []struct {
		name    string
		tlsCfg  config.TLS
		wantErr string // empty means the handshake must succeed
	}{
		{
			name:   "require accepts an untrusted certificate",
			tlsCfg: config.TLS{Mode: config.TLSRequire, CA: otherCAPath},
		},
		{
			name:   "verify-ca accepts a name mismatch on a trusted chain",
			tlsCfg: config.TLS{Mode: config.TLSVerifyCA, CA: caPath},
		},
		{
			name:    "verify-ca rejects an untrusted chain",
			tlsCfg:  config.TLS{Mode: config.TLSVerifyCA, CA: otherCAPath},
			wantErr: "verifying server certificate chain",
		},
		{
			name:    "verify-full rejects a name mismatch",
			tlsCfg:  config.TLS{Mode: config.TLSVerifyFull, CA: caPath, ServerName: "elsewhere.example"},
			wantErr: "certificate is valid for localhost, not elsewhere.example",
		},
		{
			name:    "verify-full rejects a certificate that does not cover the host",
			tlsCfg:  config.TLS{Mode: config.TLSVerifyFull, CA: caPath},
			wantErr: "cannot validate certificate for 127.0.0.1",
		},
		{
			name:   "verify-full accepts the name from server_name",
			tlsCfg: config.TLS{Mode: config.TLSVerifyFull, CA: caPath, ServerName: "localhost"},
		},
		{
			name:    "verify-full rejects an untrusted chain",
			tlsCfg:  config.TLS{Mode: config.TLSVerifyFull, CA: otherCAPath, ServerName: "localhost"},
			wantErr: "certificate signed by unknown authority",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handshake(t, ln, Target{Config: config.Connection{Name: "c", TLS: &tt.tlsCfg}})
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("handshake failed: %v", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("handshake succeeded, want an error mentioning %q", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Errorf("error = %q, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestTLSHandshakeWithClientCertificate(t *testing.T) {
	ca := newAuthority(t)
	serverCert, serverKey := ca.issue(t, "d9s test server", "localhost")
	pair, err := tls.X509KeyPair(serverCert, serverKey)
	if err != nil {
		t.Fatalf("server keypair: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca.pem) {
		t.Fatal("adding the test CA to the client pool")
	}
	ln := tlsServer(t, pair, roots)

	clientCert, clientKey := ca.issue(t, "d9s client")
	caPath := writePEM(t, "ca.pem", ca.pem)

	t.Run("presented from 1password", func(t *testing.T) {
		err := handshake(t, ln, Target{
			Config: config.Connection{Name: "c", TLS: &config.TLS{
				Mode:       config.TLSVerifyFull,
				CA:         caPath,
				ServerName: "localhost",
				Cert:       "op://Infra/db/cert",
				Key:        "op://Infra/db/key",
			}},
			Secrets: mapResolver{
				"op://Infra/db/cert": string(clientCert),
				"op://Infra/db/key":  string(clientKey),
			},
		})
		if err != nil {
			t.Fatalf("handshake failed: %v", err)
		}
	})

	t.Run("absent when the server demands one", func(t *testing.T) {
		err := handshake(t, ln, Target{Config: config.Connection{Name: "c", TLS: &config.TLS{
			Mode: config.TLSVerifyFull, CA: caPath, ServerName: "localhost",
		}}})
		if err == nil {
			t.Fatal("handshake succeeded without a client certificate, want an error")
		}
	})
}
