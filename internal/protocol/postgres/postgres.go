// Package postgres implements the "postgres" protocol.Protocol driver, for
// load testing a PostgreSQL server (or a database that speaks its wire
// protocol, such as CockroachDB, YugabyteDB or Aurora). It uses the pgx
// client, which is pure Go.
//
// Each call to Do runs one SQL text, read from -body, and reads every row
// it returns. The connections are kept in a pool that all users share, as an
// application's connection pool is. Waiting for a free connection counts as
// part of the measured time, so give -opt pool at least as many connections
// as users when you want to measure the server and not the queue.
//
// Options (set with -opt key=value):
//
//	username          The database user. It can also be given in the URL.
//	password_env      The name of an environment variable that holds the
//	                  password. It needs a user. The password is never put on
//	                  the command line, and it is refused in the URL.
//	database          The database. It can also be given as the path of the
//	                  URL. The default is the user name.
//	sslmode           disable, prefer (default), require, or verify-full.
//	                  With -insecure, verify-full does not check the
//	                  certificate.
//	application_name  The name the server shows for these connections
//	                  (default "vegaload").
//	pool              The most connections the run opens (default 10).
//	read_only         true to make every transaction read-only, so a load
//	                  test cannot change data by mistake.
//	query_mode        simple (default) or extended. simple sends the SQL
//	                  as one text, so it can hold several statements such as
//	                  "begin; update ... where id = $1; select ...; commit",
//	                  with or without args. VegaLoad puts the args into the
//	                  text as quoted values. extended prepares the statement
//	                  once for each connection and reuses it, as most
//	                  applications do. It runs one statement.
//	args              A JSON array of values for $1, $2 and so on, such as
//	                  '[42, "abc"]'.
//	min_rows          The result must have at least this many rows.
//	expect            Some value in the result must contain this text. Only
//	                  the first max_rows rows are checked.
//	max_rows          How many rows a scenario script gets back (default
//	                  1000). Every row is read and counted either way.
//
// The target is postgres://host[:port][/database] (or postgresql://), with
// port 5432 by default. The URL may end with ?sslmode=... and
// ?application_name=..., as in psql. Any other setting after the "?" is
// refused.
package postgres

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vegaload/vegaload/internal/protocol"
)

// Options are the -opt keys this driver accepts. The command's protocol
// table uses the same list, so the two cannot differ.
var Options = []string{
	"username", "password_env", "database", "sslmode", "application_name",
	"pool", "read_only", "query_mode", "args", "min_rows", "expect", "max_rows",
}

// connOptions are the keys that make up the connection. The rest describe
// the job of one call.
var connOptions = []string{
	"username", "password_env", "database", "sslmode", "application_name",
	"pool", "read_only", "query_mode",
}

const (
	defaultPool    = 10
	maxPool        = 1000
	defaultMaxRows = 1000
	maxMaxRows     = 1000000
)

// Driver is a PostgreSQL protocol.Protocol.
type Driver struct {
	target  protocol.Target
	timeout time.Duration

	simple bool // query_mode=simple

	// The job of the call. Empty on a connection made for a script, until
	// Call sets them.
	sql  string
	args []any
	// simpleSQL is sql with args put in, for query_mode=simple.
	simpleSQL string
	minRows   int
	expect    string
	maxRows   int

	// pool is shared by every Driver that Call makes from this one.
	pool *pgxpool.Pool
}

// New returns a ready-to-use Driver for target. target.URL is
// postgres://host[:port][/database]. The SQL is target.Body.
//
// New returns an error only for a configuration problem, never for the
// server being unreachable, which Do reports per call.
func New(target protocol.Target, timeout time.Duration) (*Driver, error) {
	return build(target, timeout, nil, false)
}

// NewConn is New for a scenario script. It makes only the connection pool:
// the server, the login and the connection options. It needs no SQL. The
// job of each call is then set with Call. password is the password, which
// the script already holds, so password_env is not used with it.
func NewConn(target protocol.Target, timeout time.Duration, password *string) (*Driver, error) {
	return build(target, timeout, password, true)
}

// Call returns a Driver for one call of a script: the same pool as d, with
// the job that opts and body describe. opts must hold the same connection
// options d was made with. The result shares d's pool, so closing it is d's
// job, not the caller's.
func (d *Driver) Call(opts map[string]string, body []byte, timeout time.Duration) (*Driver, error) {
	nd := *d
	nd.timeout = timeout
	nd.target.Body = body
	nd.target.Options = opts
	if err := nd.target.RejectUnknownOptions(Options...); err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	if err := nd.readJob(); err != nil {
		return nil, err
	}
	return &nd, nil
}

func build(target protocol.Target, timeout time.Duration, password *string, connOnly bool) (*Driver, error) {
	d := &Driver{target: target, timeout: timeout}

	u, err := url.Parse(target.URL)
	if err != nil {
		return nil, fmt.Errorf("postgres: parsing target URL: %w", err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return nil, fmt.Errorf("postgres: unsupported scheme %q, want postgres://", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, errors.New("postgres: the target URL has no host")
	}
	// A psql-style URL may carry settings after "?". sslmode and
	// application_name are honoured. Anything else is refused by name, and
	// its value is never shown, so a password pasted there cannot leak.
	urlOpts := map[string]string{}
	for k, vs := range u.Query() {
		switch k {
		case "sslmode", "application_name":
			if len(vs) != 1 {
				return nil, fmt.Errorf("postgres: the URL gives %s more than once", k)
			}
			urlOpts[k] = vs[0]
		case "password", "passfile", "sslpassword":
			return nil, fmt.Errorf("postgres: do not put %s in the URL, use -opt password_env=NAME", k)
		default:
			return nil, fmt.Errorf("postgres: the URL setting %q is not supported (use -opt for sslmode and application_name)", k)
		}
	}
	for _, k := range []string{"sslmode", "application_name"} {
		if v, ok := urlOpts[k]; ok {
			if o, has := target.Options[k]; has && o != v {
				return nil, fmt.Errorf("postgres: give %s in the URL or in -opt, not both", k)
			}
		}
	}
	port := u.Port()
	if port == "" {
		port = "5432"
	}

	allowed := Options
	if connOnly {
		allowed = connOptions
	}
	if err := target.RejectUnknownOptions(allowed...); err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}

	user := target.Option("username", "")
	if u.User != nil {
		if _, has := u.User.Password(); has {
			return nil, errors.New("postgres: do not put a password in the URL, use -opt password_env=NAME")
		}
		if name := u.User.Username(); name != "" {
			if user != "" && user != name {
				return nil, errors.New("postgres: give the user in the URL or in username, not both")
			}
			user = name
		}
	}

	db := strings.TrimPrefix(u.Path, "/")
	if v := target.Option("database", ""); v != "" {
		if db != "" && db != v {
			return nil, errors.New("postgres: give the database in the URL or in database, not both")
		}
		db = v
	}

	pw := ""
	if password != nil {
		if target.Option("password_env", "") != "" {
			return nil, errors.New("postgres: give a password or password_env, not both")
		}
		if user == "" {
			return nil, errors.New("postgres: a password needs a user (username): a password is only sent together with a user name")
		}
		pw = *password
	} else if env := target.Option("password_env", ""); env != "" {
		if user == "" {
			return nil, errors.New("postgres: password_env needs a user (username): a password is only sent together with a user name")
		}
		v, ok := os.LookupEnv(env)
		if !ok || v == "" {
			return nil, fmt.Errorf("postgres: password_env=%s, but that environment variable is not set", env)
		}
		pw = v
	}

	sslmode := target.Option("sslmode", urlOpts["sslmode"])
	if sslmode == "" {
		sslmode = "prefer"
	}
	switch sslmode {
	case "disable", "prefer", "require", "verify-full":
	default:
		return nil, fmt.Errorf("postgres: sslmode=%q, want disable, prefer, require, or verify-full", sslmode)
	}

	switch qm := target.Option("query_mode", "simple"); qm {
	case "simple":
		d.simple = true
	case "extended":
	default:
		return nil, fmt.Errorf("postgres: query_mode=%q, want simple or extended", qm)
	}

	poolSize, err := target.OptionInt("pool", defaultPool)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	if poolSize < 1 || poolSize > maxPool {
		return nil, fmt.Errorf("postgres: pool=%d, want 1 to %d", poolSize, maxPool)
	}
	readOnly, err := target.OptionBool("read_only", false)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}

	if !connOnly {
		if err := d.readJob(); err != nil {
			return nil, err
		}
	}

	// The connection string is built as a URL, so a user name or a database
	// with a special character is escaped correctly.
	dsn := url.URL{Scheme: "postgres", Host: net.JoinHostPort(u.Hostname(), port), Path: "/" + db}
	switch {
	case user != "" && pw != "":
		dsn.User = url.UserPassword(user, pw)
	case user != "":
		dsn.User = url.User(user)
	}
	q := url.Values{}
	q.Set("sslmode", sslmode)
	appName := target.Option("application_name", urlOpts["application_name"])
	if appName == "" {
		appName = "vegaload"
	}
	q.Set("application_name", appName)
	dsn.RawQuery = q.Encode()

	cfg, err := pgxpool.ParseConfig(dsn.String())
	if err != nil {
		// The text can hold the password, so it is never put in the error.
		return nil, errors.New("postgres: the connection settings are not valid (check the user, database and sslmode)")
	}
	cfg.MaxConns = int32(poolSize)
	cfg.MinConns = 0 // connect on the first call, not at start
	if d.simple {
		cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	} else {
		cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
	}
	if readOnly {
		cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	}
	if target.InsecureSkipVerify {
		skipVerify(cfg.ConnConfig.TLSConfig)
		for _, fb := range cfg.ConnConfig.Fallbacks {
			skipVerify(fb.TLSConfig)
		}
	}
	// pgx can read a password from the environment (PGPASSWORD) or from
	// ~/.pgpass. That is the standard PostgreSQL behaviour, so it stays on.

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return nil, errors.New("postgres: creating the connection pool failed")
	}
	d.pool = pool
	return d, nil
}

func skipVerify(c *tls.Config) {
	if c != nil {
		c.InsecureSkipVerify = true //nolint:gosec // opt-in, see protocol.Target
	}
}

// readJob reads the options that describe one call: the SQL, its arguments
// and the checks on the result.
func (d *Driver) readJob() error {
	t := d.target
	d.sql = string(bytes.TrimSpace(t.Body))
	if d.sql == "" {
		return errors.New("postgres: the SQL is required (pass it with -body)")
	}
	var err error
	if d.minRows, err = t.OptionInt("min_rows", 0); err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	if d.minRows < 0 {
		return fmt.Errorf("postgres: min_rows=%d, want 0 or more", d.minRows)
	}
	if d.maxRows, err = t.OptionInt("max_rows", defaultMaxRows); err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	if d.maxRows < 1 || d.maxRows > maxMaxRows {
		return fmt.Errorf("postgres: max_rows=%d, want 1 to %d", d.maxRows, maxMaxRows)
	}
	d.expect = t.Option("expect", "")
	d.args = nil
	if raw, ok := t.Options["args"]; ok {
		if d.args, err = parseArgs(raw); err != nil {
			return err
		}
	}
	d.simpleSQL = d.sql
	if d.simple && len(d.args) > 0 {
		var err error
		if d.simpleSQL, err = interpolate(d.sql, d.args); err != nil {
			return err
		}
	}
	return nil
}

// parseArgs reads the args option, a JSON array. A whole number stays a
// whole number. A nested object or array is passed as its JSON text, which
// a json or jsonb parameter accepts.
func parseArgs(raw string) ([]any, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var list []any
	if err := dec.Decode(&list); err != nil {
		return nil, errors.New(`postgres: args must be a JSON array, such as '[42, "abc"]'`)
	}
	if dec.More() {
		return nil, errors.New("postgres: args has text after the JSON array")
	}
	for i, v := range list {
		switch x := v.(type) {
		case json.Number:
			if n, err := x.Int64(); err == nil {
				list[i] = n
			} else if f, err := x.Float64(); err == nil {
				list[i] = f
			} else {
				return nil, fmt.Errorf("postgres: args[%d]: %q is not a number", i, x)
			}
		case map[string]any, []any:
			b, err := json.Marshal(x)
			if err != nil {
				return nil, fmt.Errorf("postgres: args[%d]: %w", i, err)
			}
			list[i] = string(b)
		}
	}
	return list, nil
}

// Name implements protocol.Protocol.
func (d *Driver) Name() string { return "postgres" }

// Close implements protocol.Protocol. It closes the connection pool.
func (d *Driver) Close() error {
	if d.pool != nil {
		d.pool.Close()
	}
	return nil
}

// Reply is what a call read, besides the result.
type Reply struct {
	// Columns are the column names of the result.
	Columns []string
	// Rows are the first max_rows rows, each as plain values in column order.
	Rows [][]any
	// RowCount is how many rows the result had, including rows past max_rows.
	RowCount int
	// RowsAffected adds up the row counts of the command tags of all the
	// statements: INSERT, UPDATE, DELETE and SELECT.
	RowsAffected int64
	// CommandTag is the tag of the last statement, such as "SELECT 3".
	CommandTag string
}

// Do implements protocol.Protocol.
func (d *Driver) Do(parent context.Context) (protocol.Result, error) {
	res, _ := d.Run(parent)
	return res, nil
}

// Run is Do, and it also returns the rows of the call. A load test does not
// need them. A scenario script does.
func (d *Driver) Run(parent context.Context) (protocol.Result, Reply) {
	ctx, cancel := context.WithTimeout(parent, d.timeout)
	defer cancel()

	var rep Reply
	sent := int64(len(d.sql))
	conn, err := d.pool.Acquire(ctx)
	if err != nil {
		return protocol.Result{BytesSent: sent, Err: d.explain(parent, ctx, err)}, rep
	}
	// A connection that timed out, or that is left inside a transaction, is
	// closed by the pool when it is released, never reused.
	defer conn.Release()

	var got int64
	if d.simple {
		got, err = d.runSimple(ctx, conn.Conn(), &rep)
	} else {
		got, err = d.runQuery(ctx, conn.Conn(), &rep)
	}
	if err == nil {
		err = d.check(rep)
	}
	if err != nil {
		return protocol.Result{BytesSent: sent, BytesReceived: got, Err: d.explain(parent, ctx, err)}, rep
	}
	return protocol.Result{Success: true, BytesSent: sent, BytesReceived: got}, rep
}

// runSimple sends the SQL (with the args put in, see interpolate) as one
// simple query. It may hold several statements, with or without args. The rows and columns are those of the last statement that
// returned columns, so "begin; ...; select ...; commit" gives the select.
func (d *Driver) runSimple(ctx context.Context, c *pgx.Conn, rep *Reply) (int64, error) {
	var got int64
	tm := c.TypeMap()
	mrr := c.PgConn().Exec(ctx, d.simpleSQL)
	for mrr.NextResult() {
		rr := mrr.ResultReader()
		fds := rr.FieldDescriptions()
		if len(fds) > 0 {
			rep.Columns = columnNames(fds)
			rep.Rows = nil
			rep.RowCount = 0
		}
		for rr.NextRow() {
			raw := rr.Values()
			for _, cell := range raw {
				got += int64(len(cell))
			}
			rep.RowCount++
			if len(rep.Rows) < d.maxRows {
				row := make([]any, len(raw))
				for i, cell := range raw {
					row[i] = decode(tm, fds[i], cell)
				}
				rep.Rows = append(rep.Rows, row)
			}
		}
		tag, err := rr.Close()
		if err != nil {
			_ = mrr.Close()
			return got, err
		}
		rep.RowsAffected += tag.RowsAffected()
		rep.CommandTag = tag.String()
	}
	return got, mrr.Close()
}

// runQuery runs one statement with pgx's own query. It is used when there
// are arguments, and in extended mode.
func (d *Driver) runQuery(ctx context.Context, c *pgx.Conn, rep *Reply) (int64, error) {
	var got int64
	rows, err := c.Query(ctx, d.sql, d.args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	rep.Columns = columnNames(rows.FieldDescriptions())
	for rows.Next() {
		for _, cell := range rows.RawValues() {
			got += int64(len(cell))
		}
		rep.RowCount++
		if len(rep.Rows) < d.maxRows {
			vals, err := rows.Values()
			if err != nil {
				return got, err
			}
			for i := range vals {
				vals[i] = normalize(vals[i])
			}
			rep.Rows = append(rep.Rows, vals)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return got, err
	}
	tag := rows.CommandTag()
	rep.RowsAffected = tag.RowsAffected()
	rep.CommandTag = tag.String()
	return got, nil
}

// check applies min_rows and expect.
func (d *Driver) check(rep Reply) error {
	if d.minRows > 0 && rep.RowCount < d.minRows {
		return fmt.Errorf("postgres: the result has %d rows, want at least %d", rep.RowCount, d.minRows)
	}
	if d.expect != "" {
		for _, row := range rep.Rows {
			for _, v := range row {
				if strings.Contains(fmt.Sprint(v), d.expect) {
					return nil
				}
			}
		}
		return fmt.Errorf("postgres: no value in the result contains %q", d.expect)
	}
	return nil
}

// explain turns an error into the one a user should read: the end of the
// run, a timeout, or the server's own message with its SQLSTATE code.
func (d *Driver) explain(parent, ctx context.Context, err error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	if ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("postgres: timed out after %s", d.timeout)
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return fmt.Errorf("postgres: %s (SQLSTATE %s)", pe.Message, pe.Code)
	}
	var ce *pgconn.ConnectError
	if errors.As(err, &ce) {
		// The text of a connect error can hold the connection string.
		return fmt.Errorf("postgres: could not connect to %s: %w", net.JoinHostPort(hostOf(d.target.URL), portOf(d.target.URL)), ce.Unwrap())
	}
	if strings.HasPrefix(err.Error(), "postgres:") {
		return err
	}
	return fmt.Errorf("postgres: %w", err)
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func portOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Port() == "" {
		return "5432"
	}
	return u.Port()
}

func columnNames(fds []pgconn.FieldDescription) []string {
	names := make([]string, len(fds))
	for i, f := range fds {
		names[i] = f.Name
	}
	return names
}

// decode turns one text cell of a simple query into a plain value.
func decode(tm *pgtype.Map, fd pgconn.FieldDescription, cell []byte) any {
	if cell == nil {
		return nil
	}
	var v any
	if err := tm.Scan(fd.DataTypeOID, fd.Format, cell, &v); err != nil {
		return string(cell)
	}
	return normalize(v)
}

// normalize turns a value pgx decoded into one a script can use: numbers,
// text, booleans and nil stay as they are, JSON stays parsed, and the
// rest (times, numeric, uuid, byte strings) become text.
func normalize(v any) any {
	switch x := v.(type) {
	case nil, bool, string, int, int8, int16, int32, int64, uint8, uint16, uint32, uint64, float32, float64:
		return x
	case []byte:
		return string(x)
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	case time.Duration:
		return x.String()
	case map[string]any, []any:
		return x
	case [16]byte:
		return formatUUID(x)
	case driver.Valuer:
		if dv, err := x.Value(); err == nil {
			return normalize(dv)
		}
	case fmt.Stringer:
		return x.String()
	}
	return fmt.Sprint(v)
}

func formatUUID(b [16]byte) string {
	h := fmt.Sprintf("%x", b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
