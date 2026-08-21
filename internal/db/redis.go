package db

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/andreim/d9s/internal/config"
)

func init() {
	Register(config.Redis, func() Driver { return &redisDriver{} })
}

// redisDriver talks to a standalone node, a sentinel-managed master, or a
// cluster. The client interface covers all three; cluster is also kept as its
// concrete type, because SCAN and DBSIZE address one node at a time and have
// to be fanned out across the masters by hand.
type redisDriver struct {
	rdb     redis.UniversalClient
	cluster *redis.ClusterClient // nil unless mode is cluster
}

func (d *redisDriver) Connect(ctx context.Context, t Target) error {
	mode := t.Config.EffectiveRedisMode()
	dbIndex := 0
	if raw := firstNonEmpty(t.Database, t.Config.Database); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("redis database must be an index 0-15, got %q", raw)
		}
		if n != 0 && mode == config.RedisCluster {
			return fmt.Errorf("redis cluster has only database 0, got %q", raw)
		}
		dbIndex = n
	}
	tlsCfg, err := tlsConfigFor(ctx, t)
	if err != nil {
		return err
	}
	dial := t.Dial
	if dial != nil && tlsCfg != nil {
		// The driver skips its own TLS setup once a dialer is set, so the
		// handshake happens in the dialer instead.
		dial = tlsDialer(dial, tlsCfg)
		tlsCfg = nil
	}
	nodes := t.Config.Nodes()
	timeout := t.Config.EffectiveConnectTimeout()

	var (
		rdb     redis.UniversalClient
		cluster *redis.ClusterClient
	)
	switch mode {
	case config.RedisCluster:
		cluster = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:       nodes, // seeds; the client discovers the rest
			Password:    t.Password,
			Dialer:      dial,
			TLSConfig:   tlsCfg,
			DialTimeout: timeout,
		})
		rdb = cluster
	case config.RedisSentinel:
		rdb = redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:    t.Config.MasterName,
			SentinelAddrs: nodes,
			Password:      t.Password,
			DB:            dbIndex,
			Dialer:        dial,
			TLSConfig:     tlsCfg,
			DialTimeout:   timeout,
		})
	default:
		rdb = redis.NewClient(&redis.Options{
			Addr:        nodes[0],
			Password:    t.Password,
			DB:          dbIndex,
			Dialer:      dial,
			TLSConfig:   tlsCfg, // nil = plaintext
			DialTimeout: timeout,
		})
	}

	pingCtx, cancel := connectContext(ctx, t)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		_ = rdb.Close()
		return connectFailed(nodes, err)
	}
	d.rdb = rdb
	d.cluster = cluster
	return nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (d *redisDriver) ListDatabases(ctx context.Context) ([]Database, error) {
	if d.cluster != nil {
		// Redis Cluster has no SELECT, so index 0 is the whole keyspace.
		return []Database{{Name: "0", Detail: d.clusterKeyCount(ctx)}}, nil
	}
	info, err := d.rdb.Info(ctx, "keyspace").Result()
	if err != nil {
		return nil, err
	}
	keys := map[int]string{}
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		// e.g. "db0:keys=42,expires=0,avg_ttl=0"
		if !strings.HasPrefix(line, "db") {
			continue
		}
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		idx, err := strconv.Atoi(name[2:])
		if err != nil {
			continue
		}
		for _, field := range strings.Split(rest, ",") {
			if v, ok := strings.CutPrefix(field, "keys="); ok {
				keys[idx] = v + " keys"
			}
		}
	}
	dbs := make([]Database, 16)
	for i := range dbs {
		dbs[i] = Database{Name: strconv.Itoa(i), Detail: keys[i]}
	}
	return dbs, nil
}

// clusterKeyCount totals DBSIZE across the masters, which is the closest
// equivalent of the per-database key count a standalone node reports.
func (d *redisDriver) clusterKeyCount(ctx context.Context) string {
	var (
		mu    sync.Mutex
		total int64
	)
	err := d.cluster.ForEachMaster(ctx, func(ctx context.Context, node *redis.Client) error {
		n, err := node.DBSize(ctx).Result()
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		total += n
		return nil
	})
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d keys", total)
}

// scanLimit caps how many keys the schema browser walks, so a huge keyspace
// cannot stall the UI.
const scanLimit = 10000

// scanKeys walks the keyspace, stopping at scanLimit. The bool reports whether
// the walk was truncated.
func (d *redisDriver) scanKeys(ctx context.Context, match string) ([]string, bool, error) {
	if d.cluster != nil {
		return d.scanCluster(ctx, match)
	}
	return scanNode(ctx, d.rdb, match)
}

// scanCluster walks every master, because SCAN covers only the slots of the
// node that serves it.
func (d *redisDriver) scanCluster(ctx context.Context, match string) ([]string, bool, error) {
	var (
		mu        sync.Mutex
		keys      []string
		truncated bool
	)
	err := d.cluster.ForEachMaster(ctx, func(ctx context.Context, node *redis.Client) error {
		found, cut, err := scanNode(ctx, node, match)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		keys = append(keys, found...)
		truncated = truncated || cut
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if len(keys) > scanLimit {
		return keys[:scanLimit], true, nil
	}
	return keys, truncated, nil
}

// scanNode walks one node's keyspace with a cursor (never KEYS), stopping at
// scanLimit. The bool reports whether the walk was truncated.
func scanNode(ctx context.Context, rdb redis.Cmdable, match string) ([]string, bool, error) {
	var (
		keys   []string
		cursor uint64
	)
	for {
		batch, next, err := rdb.Scan(ctx, cursor, match, 500).Result()
		if err != nil {
			return nil, false, err
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 {
			return keys, false, nil
		}
		if len(keys) >= scanLimit {
			return keys[:scanLimit], true, nil
		}
	}
}

func (d *redisDriver) ListTables(ctx context.Context) ([]Table, error) {
	keys, truncated, err := d.scanKeys(ctx, "*")
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, k := range keys {
		prefix, _, ok := strings.Cut(k, ":")
		if !ok {
			prefix = k
		}
		counts[prefix]++
	}
	names := make([]string, 0, len(counts))
	for prefix := range counts {
		names = append(names, prefix)
	}
	sort.Strings(names)
	tables := make([]Table, 0, len(names))
	for _, prefix := range names {
		detail := fmt.Sprintf("%d keys", counts[prefix])
		if truncated {
			detail += " (partial scan)"
		}
		tables = append(tables, Table{Name: prefix, Detail: detail})
	}
	return tables, nil
}

func (d *redisDriver) ListColumns(ctx context.Context, prefix string) ([]Column, error) {
	keys, truncated, err := d.scanKeys(ctx, prefix+"*")
	if err != nil {
		return nil, err
	}
	sort.Strings(keys)
	cols := make([]Column, 0, len(keys))
	for _, k := range keys {
		typ, err := d.rdb.Type(ctx, k).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				continue // key expired between the scan and the type lookup
			}
			return nil, err
		}
		cols = append(cols, Column{Name: k, Type: typ, Detail: ttlDetail(ctx, d.rdb, k)})
	}
	if truncated {
		cols = append(cols, Column{Name: "…", Detail: "partial scan; refine the prefix"})
	}
	return cols, nil
}

// ttlDetail renders a key's expiry for display; keys without one render empty.
func ttlDetail(ctx context.Context, rdb redis.Cmdable, key string) string {
	ttl, err := rdb.TTL(ctx, key).Result()
	if err != nil || ttl < 0 {
		return ""
	}
	return "ttl " + ttl.Truncate(time.Second).String()
}

func (d *redisDriver) Execute(ctx context.Context, statement string) (res Result) {
	res.Statement = statement
	res.Affected = -1
	res.Columns = []string{"reply"}
	start := time.Now()
	defer func() { res.Duration = time.Since(start) }()

	fields, err := splitCommandLine(statement)
	if err != nil {
		res.Err = err
		return res
	}
	if len(fields) == 0 {
		res.Err = errors.New("empty command")
		return res
	}
	args := make([]any, len(fields))
	for i, f := range fields {
		args[i] = f
	}
	val, err := d.rdb.Do(ctx, args...).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			res.Rows = [][]string{{"(nil)"}}
			return res
		}
		res.Err = err
		return res
	}
	res.Rows = redisReplyRows(val, "")
	return res
}

// redisReplyRows flattens a reply into display rows, prefixing nested array
// elements with their 1-based indexes.
func redisReplyRows(v any, prefix string) [][]string {
	switch x := v.(type) {
	case nil:
		return [][]string{{prefix + "(nil)"}}
	case []any:
		if len(x) == 0 {
			return [][]string{{prefix + "(empty array)"}}
		}
		var rows [][]string
		for i, e := range x {
			rows = append(rows, redisReplyRows(e, prefix+strconv.Itoa(i+1)+") ")...)
		}
		return rows
	case map[any]any:
		if len(x) == 0 {
			return [][]string{{prefix + "(empty map)"}}
		}
		entries := make([]string, 0, len(x))
		for k, e := range x {
			entries = append(entries, fmt.Sprint(k)+": "+fmt.Sprint(e))
		}
		sort.Strings(entries)
		rows := make([][]string, len(entries))
		for i, e := range entries {
			rows[i] = []string{prefix + e}
		}
		return rows
	default:
		return [][]string{{prefix + fmt.Sprint(x)}}
	}
}

// splitCommandLine splits a raw command line like a shell would: fields
// separated by whitespace, with double quotes (honoring \n, \t, \r, and
// backslash escapes), single quotes (literal), and bare backslash escapes.
func splitCommandLine(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inArg := false
	flush := func() {
		if inArg {
			args = append(args, cur.String())
			cur.Reset()
			inArg = false
		}
	}
	for i := 0; i < len(s); {
		switch c := s[i]; c {
		case ' ', '\t':
			flush()
			i++
		case '\'':
			inArg = true
			i++
			closed := false
			for i < len(s) {
				if s[i] == '\'' {
					i++
					closed = true
					break
				}
				cur.WriteByte(s[i])
				i++
			}
			if !closed {
				return nil, errors.New("unterminated single quote")
			}
		case '"':
			inArg = true
			i++
			closed := false
			for i < len(s) {
				if s[i] == '\\' && i+1 < len(s) {
					i++
					switch s[i] {
					case 'n':
						cur.WriteByte('\n')
					case 't':
						cur.WriteByte('\t')
					case 'r':
						cur.WriteByte('\r')
					default:
						cur.WriteByte(s[i])
					}
					i++
					continue
				}
				if s[i] == '"' {
					i++
					closed = true
					break
				}
				cur.WriteByte(s[i])
				i++
			}
			if !closed {
				return nil, errors.New("unterminated double quote")
			}
		case '\\':
			inArg = true
			if i+1 < len(s) {
				cur.WriteByte(s[i+1])
				i += 2
			} else {
				cur.WriteByte(c)
				i++
			}
		default:
			inArg = true
			cur.WriteByte(c)
			i++
		}
	}
	flush()
	return args, nil
}

func (d *redisDriver) Close() error {
	if d.rdb == nil {
		return nil
	}
	return d.rdb.Close()
}
