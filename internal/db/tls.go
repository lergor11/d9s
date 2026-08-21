package db

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/lergor11/d9s/internal/config"
)

// SecretResolver resolves an `op://` or `${ENV}` reference to its value.
// *secrets.Resolver implements it; adapters keep the interface so certificate
// material can be faked in tests.
type SecretResolver interface {
	// Resolve returns the value behind ref, which may be a literal.
	Resolve(ctx context.Context, ref string) (string, error)
}

// tlsConfigFor builds the TLS configuration for a target, resolving any
// certificate material into memory. It returns nil in disable mode, which the
// adapters pass on to their drivers to mean plaintext.
func tlsConfigFor(ctx context.Context, t Target) (*tls.Config, error) {
	mode := t.Config.EffectiveTLSMode()
	if mode == config.TLSDisable {
		return nil, nil
	}
	var settings config.TLS
	if t.Config.TLS != nil {
		settings = *t.Config.TLS
	}

	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if settings.Cert != "" {
		cert, err := clientCertificate(ctx, t.Secrets, settings)
		if err != nil {
			return nil, err
		}
		cfg.Certificates = []tls.Certificate{cert}
	}

	switch mode {
	case config.TLSRequire:
		// Encryption without authentication, by explicit request; the
		// connection list flags such connections as unverified.
		cfg.InsecureSkipVerify = true
	case config.TLSVerifyCA:
		roots, err := rootPool(ctx, t.Secrets, settings)
		if err != nil {
			return nil, err
		}
		// Go's built-in verification always checks the hostname too, so the
		// chain is verified explicitly instead. An untrusted chain still
		// aborts the handshake; only the name check is skipped.
		cfg.InsecureSkipVerify = true
		cfg.VerifyPeerCertificate = chainVerifier(roots)
	case config.TLSVerifyFull:
		roots, err := rootPool(ctx, t.Secrets, settings)
		if err != nil {
			return nil, err
		}
		cfg.RootCAs = roots
		cfg.ServerName = settings.ServerName
		if cfg.ServerName == "" {
			cfg.ServerName = t.Config.Host
		}
	default:
		return nil, fmt.Errorf("connection %q: unknown tls mode %q", t.Config.Name, mode)
	}
	return cfg, nil
}

// tlsDialer wraps a raw dialer so every connection it returns has completed a
// TLS handshake. The ClickHouse and Redis drivers negotiate TLS only in their
// own default dialers, so a tunneled connection has to do it here; pgx layers
// TLS over its DialFunc itself and needs no wrapping.
func tlsDialer(dial DialContextFunc, cfg *tls.Config) DialContextFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		raw, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		conn := tls.Client(raw, cfg)
		if err := conn.HandshakeContext(ctx); err != nil {
			_ = raw.Close()
			return nil, fmt.Errorf("tls handshake with %s: %w", addr, err)
		}
		return conn, nil
	}
}

// rootPool returns the configured CA roots, or nil for the system roots.
func rootPool(ctx context.Context, res SecretResolver, settings config.TLS) (*x509.CertPool, error) {
	if settings.CA == "" {
		return nil, nil
	}
	pem, err := material(ctx, res, "ca", settings.CA)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("tls ca %s: no PEM certificate found", settings.CA)
	}
	return pool, nil
}

// clientCertificate loads the client keypair used for mutual TLS.
func clientCertificate(ctx context.Context, res SecretResolver, settings config.TLS) (tls.Certificate, error) {
	certPEM, err := material(ctx, res, "cert", settings.Cert)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM, err := material(ctx, res, "key", settings.Key)
	if err != nil {
		return tls.Certificate{}, err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("tls client keypair: %w", err)
	}
	return cert, nil
}

// material reads one piece of certificate material: a secret reference goes
// through the resolver, anything else is a filesystem path. Neither result is
// ever written back to disk.
func material(ctx context.Context, res SecretResolver, field, ref string) ([]byte, error) {
	if config.IsOpRef(ref) || config.IsEnvRef(ref) {
		if res == nil {
			return nil, fmt.Errorf("tls %s %s: no secret resolver available", field, ref)
		}
		val, err := res.Resolve(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("tls %s %s: %w", field, ref, err)
		}
		if val == "" {
			return nil, fmt.Errorf("tls %s %s: resolved to an empty value", field, ref)
		}
		return []byte(val), nil
	}
	pem, err := os.ReadFile(ref)
	if err != nil {
		return nil, fmt.Errorf("tls %s: %w", field, err)
	}
	return pem, nil
}

// chainVerifier validates the presented chain against roots (system roots when
// nil) without checking the hostname, which is what verify-ca means.
func chainVerifier(roots *x509.CertPool) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return errors.New("tls: server presented no certificate")
		}
		certs := make([]*x509.Certificate, len(rawCerts))
		for i, raw := range rawCerts {
			cert, err := x509.ParseCertificate(raw)
			if err != nil {
				return fmt.Errorf("tls: parsing server certificate: %w", err)
			}
			certs[i] = cert
		}
		opts := x509.VerifyOptions{Roots: roots, Intermediates: x509.NewCertPool()}
		for _, cert := range certs[1:] {
			opts.Intermediates.AddCert(cert)
		}
		if _, err := certs[0].Verify(opts); err != nil {
			return fmt.Errorf("tls: verifying server certificate chain: %w", err)
		}
		return nil
	}
}
