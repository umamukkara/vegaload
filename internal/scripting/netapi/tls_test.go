package netapi

import (
	"context"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol/ftp/ftptest"
	"github.com/vegaload/vegaload/internal/protocol/mysql/mysqltest"
	"github.com/vegaload/vegaload/internal/protocol/postgres/postgrestest"
	"github.com/vegaload/vegaload/internal/protocol/rabbitmq/rabbitmqtest"
	"github.com/vegaload/vegaload/internal/protocol/redis/redistest"
	"github.com/vegaload/vegaload/internal/tlsconf/tlstest"
)

func TestScriptTLSFiles_ReachTheConnection(t *testing.T) {
	m := tlstest.New(t)
	c := NewProtoClient(nil, 3*time.Second)
	defer c.Close()

	rs := redistest.StartTLSConfig(t, nil, m.ServerConfig(false))
	r, err := c.Redis(context.Background(), ProtoCall{
		URL:  rs.URL(),
		Body: []byte("PING"),
		Options: map[string]string{
			"tls": "true", "ca_file": m.CACertPath, "tls_server_name": tlstest.Name,
		},
	})
	if err != nil || !r.OK {
		t.Fatalf("redis: %v %+v", err, r)
	}
	if _, err := c.Redis(context.Background(), ProtoCall{URL: rs.URL(), Body: []byte("PING"), Options: map[string]string{"nope": "1"}}); err == nil {
		t.Fatal("unknown redis option was accepted")
	}

	rb := rabbitmqtest.StartTLSConfig(t, m.ServerConfig(false))
	r, err = c.RabbitMQ(context.Background(), "publish", ProtoCall{
		URL:  rb.URL(),
		Body: []byte("x"),
		Options: map[string]string{
			"mandatory": "false", "ca_file": m.CACertPath, "tls_server_name": tlstest.Name,
		},
	})
	if err != nil || !r.OK {
		t.Fatalf("rabbitmq: %v %+v", err, r)
	}

	fs := ftptest.StartTLSConfig(t, m.ServerConfig(false))
	r, err = c.FTP(context.Background(), "connect", ProtoCall{
		URL: fs.URL(),
		Options: map[string]string{
			"tls": "explicit", "ca_file": m.CACertPath, "tls_server_name": tlstest.Name,
		},
	})
	if err != nil || !r.OK {
		t.Fatalf("ftp: %v %+v", err, r)
	}

	ms := mysqltest.StartTLS(t, func(string) []mysqltest.Result {
		return []mysqltest.Result{{Columns: []mysqltest.Column{mysqltest.Int("id")}, Rows: [][]any{{"1"}}}}
	}, m.ServerConfig(false))
	r, err = c.MySQL(context.Background(), ProtoCall{
		URL:  ms.URL(),
		Body: []byte("select 1"),
		Options: map[string]string{
			"username": "app", "tls": "true", "ca_file": m.CACertPath, "tls_server_name": tlstest.Name,
		},
	})
	if err != nil || !r.OK {
		t.Fatalf("mysql: %v %+v", err, r)
	}

	ps := postgrestest.StartTLS(t, func(string) []postgrestest.Result {
		return []postgrestest.Result{{Columns: []postgrestest.Column{postgrestest.Int("id")}, Rows: [][]any{{"1"}}, Tag: "SELECT 1"}}
	}, m.ServerConfig(false))
	r, err = c.Postgres(context.Background(), ProtoCall{
		URL:  ps.URL(),
		Body: []byte("select 1"),
		Options: map[string]string{
			"username": "app", "sslmode": "verify-full", "ca_file": m.CACertPath, "tls_server_name": tlstest.Name,
		},
	})
	if err != nil || !r.OK {
		t.Fatalf("postgres: %v %+v", err, r)
	}
}
