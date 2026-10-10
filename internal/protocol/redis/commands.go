package redis

import (
	"fmt"
	"strings"
)

// The lists below are the Redis commands this driver treats as reads, as
// admin, or as commands it will not send. They were chosen from the command
// flags a server reports (a command with no write flag and no admin flag is
// a read; the read-only variants such as eval_ro stay reads). A live test
// checks the same list with COMMAND INFO when VEGALOAD_TEST_REDIS is set.
// A command that is on none of these lists is a write, including a command
// this file has never heard of, so a new server command cannot sneak through.

// neverCommands change the connection, or they are part of a feature this
// driver does not run (subscriptions, WATCH, replication). They are refused
// even when allow_writes and allow_admin are set. multi, exec and discard
// are here because transaction=true sends MULTI and EXEC itself.
var neverCommands = map[string]string{
	"select":       "it changes the connection (use -opt database=N)",
	"auth":         "it changes the connection (use -opt password_env=NAME)",
	"hello":        "it changes the connection (use -opt protocol=2 or protocol=3)",
	"reset":        "it changes the connection",
	"client":       "it changes the connection",
	"multi":        "it changes the connection (use -opt transaction=true)",
	"exec":         "it changes the connection (use -opt transaction=true)",
	"discard":      "it changes the connection (use -opt transaction=true)",
	"watch":        "WATCH is not available",
	"unwatch":      "WATCH is not available",
	"subscribe":    "subscribing is not available yet",
	"psubscribe":   "subscribing is not available yet",
	"ssubscribe":   "subscribing is not available yet",
	"unsubscribe":  "subscribing is not available yet",
	"punsubscribe": "subscribing is not available yet",
	"sunsubscribe": "subscribing is not available yet",
	"monitor":      "it changes the connection",
	"quit":         "it changes the connection",
	"readonly":     "it changes the connection",
	"readwrite":    "it changes the connection",
	"sync":         "it changes the connection",
	"psync":        "it changes the connection",
	"replconf":     "it changes the connection",
}

// readCommands are always allowed. Names are lower case.
var readCommands = map[string]bool{
	"get": true, "mget": true, "strlen": true, "getrange": true, "exists": true,
	"type": true, "ttl": true, "pttl": true, "expiretime": true, "pexpiretime": true,
	"randomkey": true, "dbsize": true, "scan": true, "keys": true, "dump": true,
	"lcs": true, "getbit": true, "bitcount": true, "bitpos": true, "bitfield_ro": true,
	"sort_ro": true,

	"hget": true, "hmget": true, "hgetall": true, "hexists": true, "hlen": true,
	"hkeys": true, "hvals": true, "hstrlen": true, "hrandfield": true, "hscan": true,

	"lindex": true, "llen": true, "lrange": true, "lpos": true,

	"sismember": true, "smismember": true, "scard": true, "smembers": true,
	"srandmember": true, "sscan": true, "sinter": true, "sunion": true, "sdiff": true,
	"sintercard": true,

	"zscore": true, "zmscore": true, "zcard": true, "zcount": true, "zlexcount": true,
	"zrange": true, "zrangebyscore": true, "zrangebylex": true, "zrevrange": true,
	"zrevrangebyscore": true, "zrevrangebylex": true, "zrank": true, "zrevrank": true,
	"zscan": true, "zrandmember": true, "zdiff": true, "zinter": true, "zunion": true,
	"zintercard": true,

	"geopos": true, "geodist": true, "geohash": true, "geosearch": true,
	"georadius_ro": true, "georadiusbymember_ro": true, "pfcount": true,

	"xlen": true, "xrange": true, "xrevrange": true, "xread": true,

	"eval_ro": true, "evalsha_ro": true, "fcall_ro": true,

	"ping": true, "echo": true, "time": true, "info": true, "role": true, "lolwut": true,
}

// readSubs are the subcommands of a container that stay reads. Any other
// subcommand is a write, or an admin command when it is listed in adminSubs.
// acl is special: only whoami is a read, and every other acl subcommand is admin.
var readSubs = map[string]map[string]bool{
	"object":  {"encoding": true, "freq": true, "idletime": true, "refcount": true},
	"memory":  {"usage": true},
	"config":  {"get": true},
	"command": {"count": true, "info": true, "docs": true, "list": true, "getkeys": true},
	"slowlog": {"get": true, "len": true},
	"pubsub":  {"channels": true, "numsub": true, "numpat": true, "shardchannels": true, "shardnumsub": true},
	"xinfo":   {"stream": true, "groups": true, "consumers": true},
	"acl":     {"whoami": true},
}

// adminCommands need allow_writes and allow_admin. acl is also a container;
// its subcommands are classified in refuse before this map is used.
var adminCommands = map[string]bool{
	"flushall": true, "flushdb": true, "swapdb": true, "shutdown": true,
	"debug": true, "save": true, "bgsave": true, "bgrewriteaof": true,
	"replicaof": true, "slaveof": true, "failover": true, "cluster": true,
	"migrate": true, "module": true, "acl": true,
}

// adminSubs are container subcommands that need allow_writes and allow_admin.
var adminSubs = map[string]map[string]bool{
	"config":   {"set": true, "rewrite": true, "resetstat": true},
	"script":   {"flush": true, "kill": true},
	"function": {"flush": true, "delete": true, "restore": true, "kill": true},
}

func isContainer(name string) bool {
	_, ok := readSubs[name]
	if ok {
		return true
	}
	_, ok = adminSubs[name]
	return ok
}

// refuse reports why cmd must not be sent. A nil error means the command
// may go. Nothing is sent when this returns an error.
func refuse(cmd command, allowWrites, allowAdmin, script bool) error {
	name := cmd.name
	if hint, never := neverCommands[name]; never {
		return fmt.Errorf("redis: command %s is not supported: %s", strings.ToUpper(name), hint)
	}

	sub := ""
	if len(cmd.argv) > 1 {
		sub = strings.ToLower(cmd.argv[1])
	}
	label := strings.ToUpper(name)
	if isContainer(name) && sub != "" {
		label = label + " " + strings.ToUpper(sub)
	}

	switch classify(name, sub) {
	case classRead:
		return nil
	case classAdmin:
		if allowWrites && allowAdmin {
			return nil
		}
		return fmt.Errorf("redis: command %s needs allow_admin=true as well as allow_writes=true", label)
	default:
		if allowWrites {
			return nil
		}
		way := "use -opt allow_writes=true"
		if script {
			way = "pass allow_writes: true in the connection options"
		}
		return fmt.Errorf("redis: command %s is not allowed: VegaLoad is read-only by default: %s to allow writes", label, way)
	}
}

type class int

const (
	classWrite class = iota
	classRead
	classAdmin
)

func classify(name, sub string) class {
	if isContainer(name) {
		if readSubs[name][sub] {
			return classRead
		}
		if name == "acl" {
			// whoami is the only acl read. It was handled above.
			return classAdmin
		}
		if adminSubs[name][sub] {
			return classAdmin
		}
		return classWrite
	}
	if adminCommands[name] {
		return classAdmin
	}
	if readCommands[name] {
		return classRead
	}
	return classWrite
}
