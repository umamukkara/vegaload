---
title: "Test a Kafka cluster"
description: "The `kafka` driver uses a pure Go client, so there is nothing to install."
sidebar_position: 4
---

# Test a Kafka cluster

The `kafka` driver uses a pure Go client, so there is nothing to install.
The target is `kafka://host:9092`, or `kafkas://host:9093` for TLS. `kafkas://` takes the four options under "TLS with your own CA". The job
of each iteration is set with `-opt mode=...`:

```
# Produce: send records and wait for the broker's ack (the default mode).
vegaload run -target kafka://127.0.0.1:9092 -protocol kafka -body '{"order":1}' \
  -opt topic=orders -opt key='user-{id}' -opt acks=all -vus 20 -duration 30s

# Roundtrip: produce, then read exactly those records back.
vegaload run -target kafka://127.0.0.1:9092 -protocol kafka -body 'ping {id}' \
  -opt mode=roundtrip -opt topic=orders -vus 10 -duration 30s

# Consume: read 5 records from the start of the topic and check their text.
vegaload run -target kafka://127.0.0.1:9092 -protocol kafka \
  -opt mode=consume -opt topic=orders -opt count=5 -opt expect=order

# Admin: create and delete a topic, over and over. {id} makes each name new.
vegaload run -target kafka://127.0.0.1:9092 -protocol kafka \
  -opt mode=admin -opt action=topic_lifecycle -opt topic='load-{id}' -opt partitions=3
```

The options are `mode` (`produce`, `consume`, `roundtrip`, `admin`), `topic`,
`key`, `acks` (`all`, `leader`, `none`), `compression` (`none`, `gzip`,
`snappy`, `lz4`, `zstd`), `count`, `expect`, `from` (`start`, `end`), `action`
(`list_topics`, `create_topic`, `delete_topic`, `topic_lifecycle`,
`list_groups`, `describe_cluster`), `partitions`, `replication`, `sasl`
(`plain`, `scram-sha-256`, `scram-sha-512`), `username`, `password_env` and
`client_id`. Put the SASL password in an environment variable and pass its
name with `-opt password_env=NAME`, so it is not on the command line. An
option that the chosen mode does not use is an error.

Some points to know:

- Produce, roundtrip and admin share one client between all users, like a
  real producer. Consume, and the reading half of roundtrip, open a new
  connection for each iteration, so that time is part of the result.
- Roundtrip reads back the exact partition and offset it wrote. It does not
  use consumer groups, so it does not measure group rebalancing. Consumer
  groups are not part of this driver yet.
- The first broker is the target you give. The client then connects to the
  broker addresses that the cluster announces. The host allowlist checks only
  the first address, so make sure the announced brokers are ones you may test.
- The topic must exist, unless the broker creates topics on its own. Use the
  admin `create_topic` action first.
