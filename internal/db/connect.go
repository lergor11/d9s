package db

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/lergor11/d9s/internal/config"
)

// connectContext bounds the connect and handshake phase with the connection's
// configured timeout, so an unreachable host fails on schedule instead of
// hanging until the caller's own deadline. Every adapter uses it, which is why
// none of them carry a timeout of their own.
func connectContext(ctx context.Context, t Target) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, t.Config.EffectiveConnectTimeout())
}

// address renders one endpoint of a connection for a driver: host:port, or the
// bare directory when the host is a unix socket.
func address(conn config.Connection) string {
	if conn.IsUnixSocket() {
		return conn.Host
	}
	return net.JoinHostPort(conn.Host, strconv.Itoa(conn.Port))
}

// connectFailed wraps a connect error so the message names the endpoint even
// when the cause is a bare context deadline.
func connectFailed(endpoints []string, err error) error {
	return fmt.Errorf("connecting to %s: %w", strings.Join(endpoints, ", "), err)
}
