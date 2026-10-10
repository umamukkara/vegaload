package rabbitmq

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

func (d *Driver) publish(ctx context.Context, l leash, id string) (sent int64, err error) {
	conn, own, err := d.connection(ctx)
	if err != nil {
		return 0, err
	}
	if own {
		defer conn.Close()
	}
	l.use(nil, conn)
	var ch *amqp.Channel
	var rets chan amqp.Return
	var closed *atomic.Pointer[amqp.Error]
	var pooled *pubCh
	defer func() {
		if pooled == nil {
			return
		}
		// err is the named result, so this sees the error the call returned.
		// A timed-out or failed call must not put its channel back.
		d.link.giveBack(pooled, err == nil && ctx.Err() == nil && ch != nil && !ch.IsClosed())
	}()
	if d.confirm && !d.perCall {
		pooled, err = d.link.borrow(conn)
		if err != nil {
			return 0, err
		}
		ch, rets = pooled.ch, pooled.rets
		closed = pooled.close
	} else {
		ch, err = conn.Channel()
		if err != nil {
			return 0, err
		}
		defer ch.Close()
		if d.confirm {
			if err = ch.Confirm(false); err != nil {
				return 0, err
			}
		}
		rets = make(chan amqp.Return, d.count)
		ch.NotifyReturn(rets)
		closed = &atomic.Pointer[amqp.Error]{}
		watchClose(ch, closed)
	}
	l.use(ch, conn)
	drainReturns(rets)

	var confirms []*amqp.DeferredConfirmation
	for n := 1; n <= d.count; n++ {
		body := []byte(applyTokens(string(d.target.Body), id, n))
		key := applyTokens(d.routingKey, id, n)
		pub := d.publishing(id, n, body)
		if d.confirm {
			dc, err := ch.PublishWithDeferredConfirmWithContext(ctx, d.exchange, key, d.mandatory, false, pub)
			if err != nil {
				return sent, err
			}
			confirms = append(confirms, dc)
		} else if err := ch.PublishWithContext(ctx, d.exchange, key, false, false, pub); err != nil {
			return sent, err
		}
		sent += int64(len(body))
	}
	if !d.confirm {
		return sent, nil
	}
	for i, dc := range confirms {
		if dc == nil {
			continue
		}
		ack, err := dc.WaitContext(ctx)
		if err != nil {
			return sent, err
		}
		if !ack {
			if e := channelDeath(ch, closed); e != nil {
				return sent, e
			}
			return sent, nackErr()
		}
		_ = i
	}
	if r, ok := drainReturns(rets); ok {
		return sent, notRouted(r.Exchange, r.RoutingKey)
	}
	return sent, nil
}

func (d *Driver) consume(ctx context.Context, l leash, id string) (int64, []Message, error) {
	conn, own, err := d.connection(ctx)
	if err != nil {
		return 0, nil, err
	}
	if own {
		defer conn.Close()
	}
	l.use(nil, conn)
	name := applyTokens(d.queue, id, 1)
	chk, err := conn.Channel()
	if err != nil {
		return 0, nil, err
	}
	l.use(chk, conn)
	if _, err := chk.QueueInspect(name); err != nil {
		_ = chk.Close()
		return 0, nil, err
	}
	_ = chk.Close()

	ch, err := conn.Channel()
	if err != nil {
		return 0, nil, err
	}
	defer ch.Close()
	l.use(ch, conn)
	if err := ch.Qos(d.prefetch, 0, false); err != nil {
		return 0, nil, err
	}
	tag := "vegaload-" + id
	ds, err := ch.Consume(name, tag, false, false, false, false, nil)
	if err != nil {
		return 0, nil, err
	}
	var (
		msgs []Message
		got  int64
		last *amqp.Delivery
		bad  error
	)
	for len(msgs) < d.count && bad == nil {
		select {
		case <-ctx.Done():
			bad = ctx.Err()
		case m, ok := <-ds:
			if !ok {
				bad = fmt.Errorf("rabbitmq: the consumer stopped")
				break
			}
			last = &m
			msgs = append(msgs, Message{
				Exchange: m.Exchange, RoutingKey: m.RoutingKey, Body: string(m.Body),
				MessageID: m.MessageId, Redelivered: m.Redelivered,
			})
			got += int64(len(m.Body))
			if d.expect != "" && !strings.Contains(string(m.Body), d.expect) {
				bad = fmt.Errorf("rabbitmq: the message does not contain %q", d.expect)
			}
		}
	}
	if bad == nil && last != nil {
		if d.ack == "ack" {
			bad = last.Ack(true)
		} else {
			bad = last.Nack(true, true)
		}
	}
	_ = ch.Cancel(tag, false)
	return got, msgs, bad
}

func (d *Driver) roundtrip(ctx context.Context, l leash, id string) (sent, got int64, msgs []Message, err error) {
	conn, own, err := d.connection(ctx)
	if err != nil {
		return 0, 0, nil, err
	}
	if own {
		defer conn.Close()
	}
	l.use(nil, conn)
	ch, err := conn.Channel()
	if err != nil {
		return 0, 0, nil, err
	}
	defer ch.Close()
	q, err := ch.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		return 0, 0, nil, err
	}
	defer d.deleteQueue(conn, ch, q.Name)
	if d.exchange != "" {
		key := applyTokens(d.bindKey, id, 1)
		if err := ch.QueueBind(q.Name, key, d.exchange, false, nil); err != nil {
			return 0, 0, nil, err
		}
	}
	if err := ch.Confirm(false); err != nil {
		return 0, 0, nil, err
	}
	l.use(ch, conn)
	rets := make(chan amqp.Return, d.count)
	ch.NotifyReturn(rets)
	tag := "vegaload-" + id
	ds, err := ch.Consume(q.Name, tag, true, false, false, false, nil)
	if err != nil {
		return 0, 0, nil, err
	}
	var confirms []*amqp.DeferredConfirmation
	want := map[string]bool{}
	for n := 1; n <= d.count; n++ {
		body := []byte(applyTokens(string(d.target.Body), id, n))
		key := q.Name
		if d.exchange != "" {
			key = applyTokens(d.routingKey, id, n)
		}
		mid := id + "-" + fmt.Sprint(n)
		want[mid] = true
		pub := d.publishing(id, n, body)
		dc, err := ch.PublishWithDeferredConfirmWithContext(ctx, d.exchange, key, true, false, pub)
		if err != nil {
			return sent, 0, nil, err
		}
		confirms = append(confirms, dc)
		sent += int64(len(body))
	}
	seen := map[string]bool{}
	confDone := make(chan error, 1)
	go func() {
		for _, dc := range confirms {
			if dc == nil {
				continue
			}
			ack, err := dc.WaitContext(ctx)
			if err != nil {
				confDone <- err
				return
			}
			if !ack {
				confDone <- nackErr()
				return
			}
		}
		confDone <- nil
	}()
	defer func() { _ = ch.Cancel(tag, false) }()
	confirmed := false
	for len(seen) < d.count {
		select {
		case <-ctx.Done():
			return sent, got, msgs, ctx.Err()
		case r := <-rets:
			return sent, got, msgs, notRouted(r.Exchange, r.RoutingKey)
		case err := <-confDone:
			confirmed = true
			if err != nil {
				return sent, got, msgs, err
			}
		case m, ok := <-ds:
			if !ok {
				return sent, got, msgs, fmt.Errorf("rabbitmq: the consumer stopped")
			}
			if !want[m.MessageId] || seen[m.MessageId] {
				continue
			}
			if d.expect != "" && !strings.Contains(string(m.Body), d.expect) {
				return sent, got, msgs, fmt.Errorf("rabbitmq: the message does not contain %q", d.expect)
			}
			seen[m.MessageId] = true
			msgs = append(msgs, Message{
				Exchange: m.Exchange, RoutingKey: m.RoutingKey, Body: string(m.Body),
				MessageID: m.MessageId, Redelivered: m.Redelivered,
			})
			got += int64(len(m.Body))
		}
	}
	if !confirmed {
		select {
		case <-ctx.Done():
			return sent, got, msgs, ctx.Err()
		case err := <-confDone:
			if err != nil {
				return sent, got, msgs, err
			}
		}
	}
	return sent, got, msgs, nil
}

func (d *Driver) deleteQueue(conn *amqp.Connection, ch *amqp.Channel, name string) {
	if ch != nil && !ch.IsClosed() {
		_, _ = ch.QueueDelete(name, false, false, false)
		return
	}
	if conn == nil || conn.IsClosed() {
		return
	}
	c2, err := conn.Channel()
	if err != nil {
		return
	}
	defer c2.Close()
	done := make(chan struct{})
	go func() {
		_, _ = c2.QueueDelete(name, false, false, false)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		_ = c2.Close()
	}
}

func (d *Driver) admin(ctx context.Context, l leash, id string) (string, error) {
	_ = ctx
	conn, own, err := d.connection(ctx)
	if err != nil {
		return "", err
	}
	if own {
		defer conn.Close()
	}
	l.use(nil, conn)
	ch, err := conn.Channel()
	if err != nil {
		return "", err
	}
	defer ch.Close()
	l.use(ch, conn)
	switch d.action {
	case "queue_info":
		name := applyTokens(d.queue, id, 1)
		q, err := ch.QueueInspect(name)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("queue=%s messages=%d consumers=%d", q.Name, q.Messages, q.Consumers), nil
	case "queue_declare":
		name := applyTokens(d.queue, id, 1)
		var args amqp.Table
		if d.queueType != "" {
			args = amqp.Table{"x-queue-type": d.queueType}
		}
		q, err := ch.QueueDeclare(name, d.durable, d.autoDelete, false, false, args)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("queue=%s messages=%d consumers=%d", q.Name, q.Messages, q.Consumers), nil
	case "queue_delete":
		name := applyTokens(d.queue, id, 1)
		n, err := ch.QueueDelete(name, false, false, false)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("queue=%s deleted=%d", name, n), nil
	case "queue_purge":
		name := applyTokens(d.queue, id, 1)
		n, err := ch.QueuePurge(name, false)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("queue=%s purged=%d", name, n), nil
	case "queue_lifecycle":
		name := applyTokens(d.queue, id, 1)
		if _, err := ch.QueueDeclare(name, d.durable, d.autoDelete, false, false, nil); err != nil {
			return "", err
		}
		if _, err := ch.QueueDelete(name, false, false, false); err != nil {
			return "", err
		}
		return "queue=" + name, nil
	case "exchange_declare":
		name := applyTokens(d.exchange, id, 1)
		if err := ch.ExchangeDeclare(name, d.exchangeType, d.durable, d.autoDelete, false, false, nil); err != nil {
			return "", err
		}
		return "exchange=" + name, nil
	case "exchange_delete":
		name := applyTokens(d.exchange, id, 1)
		if err := ch.ExchangeDelete(name, false, false); err != nil {
			return "", err
		}
		return "exchange=" + name, nil
	default:
		return "", fmt.Errorf("rabbitmq: action=%q is not known", d.action)
	}
}

func channelDeath(ch *amqp.Channel, closed *atomic.Pointer[amqp.Error]) *amqp.Error {
	if ch == nil || closed == nil || !ch.IsClosed() {
		return nil
	}
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if e := closed.Load(); e != nil {
			return e
		}
		time.Sleep(time.Millisecond)
	}
	return nil
}

func (d *Driver) publishing(id string, n int, body []byte) amqp.Publishing {
	mode := uint8(1)
	if d.persistent {
		mode = 2
	}
	pub := amqp.Publishing{
		Body:         body,
		MessageId:    id + "-" + fmt.Sprint(n),
		Timestamp:    time.Now(),
		DeliveryMode: mode,
		ContentType:  d.contentType,
		Expiration:   d.expiration,
		Headers:      d.headers,
	}
	if d.prioritySet && d.priority > 0 {
		pub.Priority = uint8(d.priority)
	}
	return pub
}
