// Package nats provides NATS JetStream client + event log semantics.
package nats

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Client wraps a NATS connection and JetStream context.
type Client struct {
	conn *nats.Conn
	js   jetstream.JetStream
}

// Connect dials NATS, verifies connectivity, and returns a ready Client.
func Connect(ctx context.Context, url, clusterID string) (*Client, error) {
	opts := []nats.Option{
		nats.Name("pulse-cell"),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
		nats.PingInterval(20 * time.Second),
		nats.MaxPingsOutstanding(2),
		nats.Timeout(10 * time.Second),
		nats.ReconnectJitter(500*time.Millisecond, 2*time.Second),
	}

	conn, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, fmt.Errorf("nats connect %s: %w", url, err)
	}

	js, err := jetstream.New(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("jetstream init: %w", err)
	}

	return &Client{conn: conn, js: js}, nil
}

// Conn returns the underlying NATS connection.
func (c *Client) Conn() *nats.Conn { return c.conn }

// JS returns the JetStream context.
func (c *Client) JS() jetstream.JetStream { return c.js }

// Close closes the NATS connection.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	c.conn.Close()
	return nil
}

// EnsureStream creates a stream if it doesn't exist.
func (c *Client) EnsureStream(ctx context.Context, name string, subjects []string) error {
	_, err := c.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     name,
		Subjects: subjects,
		Storage:  jetstream.FileStorage,
		Replicas: 1,
		MaxAge:   7 * 24 * time.Hour,
	})
	if err != nil && !errors.Is(err, jetstream.ErrStreamNameAlreadyInUse) {
		return fmt.Errorf("create stream %s: %w", name, err)
	}
	return nil
}

// Publish appends a message to a stream subject.
func (c *Client) Publish(ctx context.Context, subject string, data []byte, headers ...map[string]string) error {
	msg := &nats.Msg{
		Subject: subject,
		Data:    data,
	}
	if len(headers) > 0 {
		msg.Header = nats.Header{}
		for _, h := range headers {
			for k, v := range h {
				msg.Header.Add(k, v)
			}
		}
	}
	ack, err := c.js.PublishMsgAsync(msg)
	if err != nil {
		return fmt.Errorf("publish %s: %w", subject, err)
	}
	select {
	case <-ack.Ok():
		return nil
	case err := <-ack.Err():
		return fmt.Errorf("publish %s ack: %w", subject, err)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Subscribe creates a durable consumer and runs handler for each message.
// The consumer subscribes to all subjects in the stream (filter via subject pattern
// inside handler if needed).
func (c *Client) Subscribe(ctx context.Context, stream, consumer, subjectFilter string, handler func([]byte) error) error {
	cons, err := c.js.CreateOrUpdateConsumer(ctx, stream, jetstream.ConsumerConfig{
		Name:          consumer,
		FilterSubject: subjectFilter,
		AckPolicy:     jetstream.AckExplicitPolicy,
	})
	if err != nil {
		return fmt.Errorf("create consumer %s/%s: %w", stream, consumer, err)
	}

	iter, err := cons.Messages()
	if err != nil {
		return fmt.Errorf("consumer messages %s: %w", stream, err)
	}

	go func() {
		defer iter.Stop()
		for {
			// Honour cancellation without blocking on Next()
			select {
			case <-ctx.Done():
				return
			default:
			}
			rawMsg, err := iter.Next()
			if err != nil {
				return // context cancelled
			}
			if rawMsg == nil {
				return
			}
			if err := handler(rawMsg.Data()); err != nil {
				_ = rawMsg.Nak()
				continue
			}
			_ = rawMsg.Ack()
		}
	}()

	return nil
}

// EnsureKeyValue ensures a KeyValue bucket exists and returns it.
func (c *Client) EnsureKeyValue(ctx context.Context, bucket string, ttl time.Duration) (jetstream.KeyValue, error) {
	if c == nil || c.js == nil {
		return nil, errors.New("nats client or jetstream is nil")
	}
	kv, err := c.js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket: bucket,
		TTL:    ttl,
	})
	if err != nil {
		return nil, fmt.Errorf("ensure kv %s: %w", bucket, err)
	}
	return kv, nil
}
