package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vegaload/vegaload/internal/inputs"
	"github.com/vegaload/vegaload/internal/protocol"
	"github.com/vegaload/vegaload/internal/protocol/redis"
	"github.com/vegaload/vegaload/internal/report"
)

// dataClass says where a {data.NAME.COLUMN} placeholder is allowed.
// placeholder and fixed together are exactly the driver's options: a new
// option that is in neither list fails the classification test.
// body is true when a placeholder may appear in -body. PostgreSQL and
// MySQL keep values in -opt args, so a placeholder in the SQL is refused.
type dataClass struct {
	placeholder []string
	fixed       []string
	body        bool
}

func (c dataClass) kind(key string) string {
	for _, k := range c.placeholder {
		if k == key {
			return "placeholder"
		}
	}
	for _, k := range c.fixed {
		if k == key {
			return "fixed"
		}
	}
	return ""
}

// protocolDataClass is the one table of which -opt keys may hold a data
// placeholder. Connection keys, mode, credentials and TLS options are fixed.
var protocolDataClass = map[string]dataClass{
	"postgres": {
		placeholder: []string{"args", "min_rows", "expect", "max_rows"},
		fixed: []string{
			"username", "password_env", "database", "sslmode", "application_name",
			"pool", "allow_writes", "query_mode",
			"ca_file", "cert_file", "key_file", "tls_server_name",
		},
	},
	"mysql": {
		placeholder: []string{"args", "min_rows", "expect", "max_rows"},
		fixed: []string{
			"username", "password_env", "database", "tls",
			"pool", "allow_writes", "query_mode",
			"ca_file", "cert_file", "key_file", "tls_server_name",
		},
	},
	"redis": {
		placeholder: []string{"args", "min_rows", "expect", "max_rows"},
		fixed: []string{
			"username", "password_env", "database", "tls", "pool", "protocol",
			"allow_writes", "allow_admin", "transaction",
			"ca_file", "cert_file", "key_file", "tls_server_name",
		},
		body: true,
	},
	"kafka": {
		placeholder: []string{"topic", "key", "count", "expect", "from", "partitions", "replication"},
		fixed: []string{
			"mode", "action", "acks", "compression", "sasl", "username", "password_env", "client_id",
			"ca_file", "cert_file", "key_file", "tls_server_name",
		},
		body: true,
	},
	"rabbitmq": {
		placeholder: []string{
			"exchange", "routing_key", "queue", "bind_key", "count",
			"confirm", "mandatory", "persistent", "content_type", "priority",
			"expiration", "headers", "ack", "prefetch", "expect",
		},
		fixed: []string{
			"mode", "action", "durable", "auto_delete", "queue_type", "exchange_type",
			"username", "password_env", "vhost", "tls", "connection", "channels",
			"heartbeat", "connection_name", "allow_writes", "allow_admin",
			"ca_file", "cert_file", "key_file", "tls_server_name",
		},
		body: true,
	},
	"ftp": {
		placeholder: []string{"path", "size", "fill", "expect_size", "expect_sha256", "expect", "limit", "hidden"},
		fixed: []string{
			"mode", "username", "password_env", "tls", "tls_verify", "tls_max",
			"sessions", "connection", "data_host", "epsv", "allow_writes", "allow_admin",
			"ca_file", "cert_file", "key_file", "tls_server_name",
		},
		body: true,
	},
	"sftp": {
		placeholder: []string{"path", "size", "fill", "expect_size", "expect_sha256", "expect", "limit", "hidden"},
		fixed: []string{
			"mode", "username", "password_env", "key_file", "key_passphrase_env", "host_key",
			"connections", "connection", "allow_writes", "allow_admin", "concurrent_writes",
		},
		body: true,
	},
}

// dataPlan is parsed once, before any load. A nil plan means the run has
// no -data, and one call does no extra work.
type dataPlan struct {
	pick     string
	sets     map[string]*inputs.Set
	optTpl   map[string]inputs.Template
	bodyTpl  inputs.Template
	redis    [][]inputs.Template // set only when -body is a redis command with a placeholder
	baseOpts map[string]string
	baseBody []byte
	files    []report.DataFile
}

func prepareProtocolData(cfg *runConfig) (*dataPlan, error) {
	spec, ok := findDriver(cfg.Protocol)
	if !ok {
		return nil, fmt.Errorf("%s", unknownProtocolMessage(cfg.Protocol))
	}
	if spec.perCall == nil {
		return nil, fmt.Errorf("-data is not supported with -protocol %s yet. Use a scenario file, where data.NAME works for every protocol.", cfg.Protocol)
	}
	class, ok := protocolDataClass[cfg.Protocol]
	if !ok {
		return nil, fmt.Errorf("-protocol %s: data files are not classified", cfg.Protocol)
	}

	plan := &dataPlan{
		pick:     cfg.DataPick,
		sets:     cfg.DataSets,
		optTpl:   map[string]inputs.Template{},
		baseOpts: cfg.Target.Options,
		baseBody: cfg.Target.Body,
	}
	used := map[string]bool{}
	note := func(where string, slots []inputs.Slot) error {
		if err := inputs.CheckColumns(slots, cfg.DataSets); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		for _, s := range slots {
			used[s.Set] = true
		}
		return nil
	}

	for k, v := range cfg.Target.Options {
		tpl, err := inputs.Parse(v, k == "args")
		if err != nil {
			return nil, err
		}
		if !tpl.HasSlots() {
			continue
		}
		switch class.kind(k) {
		case "fixed":
			return nil, fmt.Errorf("-opt %s: a data placeholder is not allowed in %s", k, k)
		case "placeholder":
			if err := note("-opt "+k, tpl.Slots()); err != nil {
				return nil, err
			}
			plan.optTpl[k] = tpl
		default:
			// An unknown key is reported when the driver is built.
		}
	}

	bodyTpl, err := inputs.Parse(string(cfg.Target.Body), false)
	if err != nil {
		return nil, err
	}
	if bodyTpl.HasSlots() && !class.body {
		return nil, fmt.Errorf("-body: put the value in -opt args, so it is sent as a parameter")
	}
	if bodyTpl.HasSlots() && cfg.Protocol == "redis" {
		lines, err := redis.SplitTokens(string(cfg.Target.Body))
		if err != nil {
			return nil, err
		}
		plan.redis = make([][]inputs.Template, len(lines))
		var slots []inputs.Slot
		for i, line := range lines {
			plan.redis[i] = make([]inputs.Template, len(line))
			for j, tok := range line {
				tpl, err := inputs.Parse(tok, false)
				if err != nil {
					return nil, err
				}
				plan.redis[i][j] = tpl
				slots = append(slots, tpl.Slots()...)
			}
		}
		if len(slots) == 0 {
			return nil, fmt.Errorf("-body: a data placeholder must sit inside one argument")
		}
		if err := note("-body", slots); err != nil {
			return nil, err
		}
	} else if bodyTpl.HasSlots() {
		if err := note("-body", bodyTpl.Slots()); err != nil {
			return nil, err
		}
		plan.bodyTpl = bodyTpl
	}

	var unused []string
	for name := range cfg.DataSets {
		if !used[name] {
			unused = append(unused, name)
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		return nil, fmt.Errorf("-data %s: nothing uses it", unused[0])
	}

	if _, _, err := plan.fill(plan.sample()); err != nil {
		return nil, err
	}

	names := make([]string, 0, len(cfg.DataSets))
	for name := range cfg.DataSets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		set := cfg.DataSets[name]
		plan.files = append(plan.files, report.DataFile{Name: set.FileName(), Rows: set.Len()})
	}
	return plan, nil
}

func (p *dataPlan) sample() map[string]map[string]any {
	rows := make(map[string]map[string]any, len(p.sets))
	for name, set := range p.sets {
		rows[name] = set.First()
	}
	return rows
}

func (p *dataPlan) draw() map[string]map[string]any {
	rows := make(map[string]map[string]any, len(p.sets))
	for name, set := range p.sets {
		if p.pick == "random" {
			rows[name] = set.Random()
		} else {
			rows[name] = set.Next()
		}
	}
	return rows
}

// fill returns the options and body for one call. It does not change the
// plan. A template with no slots is not stored, so those values are copied
// as they were written.
func (p *dataPlan) fill(rows map[string]map[string]any) (map[string]string, []byte, error) {
	opts := make(map[string]string, len(p.baseOpts))
	for k, v := range p.baseOpts {
		opts[k] = v
	}
	for k, tpl := range p.optTpl {
		text, err := tpl.Fill(rows)
		if err != nil {
			return nil, nil, dataFileErr("-opt "+k, p.sets, err)
		}
		opts[k] = text
	}
	body, err := p.fillBody(rows)
	if err != nil {
		return nil, nil, err
	}
	return opts, body, nil
}

func (p *dataPlan) fillBody(rows map[string]map[string]any) ([]byte, error) {
	if p.redis != nil {
		lines := make([][]string, len(p.redis))
		for i, line := range p.redis {
			lines[i] = make([]string, len(line))
			for j, tpl := range line {
				text, err := tpl.Fill(rows)
				if err != nil {
					return nil, dataFileErr("-body", p.sets, err)
				}
				lines[i][j] = text
			}
		}
		return []byte(redis.JoinTokens(lines)), nil
	}
	if p.bodyTpl.HasSlots() {
		text, err := p.bodyTpl.Fill(rows)
		if err != nil {
			return nil, dataFileErr("-body", p.sets, err)
		}
		return []byte(text), nil
	}
	return p.baseBody, nil
}

func (p *dataPlan) filledTarget(base protocol.Target, rows map[string]map[string]any) (protocol.Target, error) {
	opts, body, err := p.fill(rows)
	if err != nil {
		return protocol.Target{}, err
	}
	out := base
	out.Options = opts
	out.Body = body
	return out, nil
}

// dataFileErr names the file and the option. The cell value is never included.
func dataFileErr(where string, sets map[string]*inputs.Set, err error) error {
	msg := err.Error()
	for name, set := range sets {
		msg = strings.ReplaceAll(msg, "data set "+name, "data file "+set.FileName())
	}
	return fmt.Errorf("%s: %s", where, msg)
}
