//go:build integration

package bus_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/andyjmorgan/sluice-gateway/internal/bus"
)

func startNATS(t *testing.T) (string, func()) {
	t.Helper()
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "nats:2.10-alpine",
		ExposedPorts: []string{"4222/tcp"},
		Cmd:          []string{"-js"},
		WaitingFor:   wait.ForLog("Server is ready").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start nats container: %v", err)
	}

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := c.MappedPort(ctx, "4222")
	if err != nil {
		t.Fatalf("mapped port: %v", err)
	}
	url := fmt.Sprintf("nats://%s:%s", host, port.Port())

	cleanup := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = c.Terminate(shutdownCtx)
	}
	return url, cleanup
}

func newJSClient(t *testing.T, url string) (jetstream.JetStream, *nats.Conn, func()) {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		t.Fatalf("jetstream new: %v", err)
	}
	return js, nc, func() {
		nc.Close()
	}
}

func TestIntegration_EnsureStreamIdempotent(t *testing.T) {
	url, stop := startNATS(t)
	defer stop()

	js, _, close := newJSClient(t, url)
	defer close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := bus.EnsureStream(ctx, js, bus.DefaultStreamName, []string{"gateway.>"}); err != nil {
		t.Fatalf("first EnsureStream: %v", err)
	}
	if err := bus.EnsureStream(ctx, js, bus.DefaultStreamName, []string{"gateway.>"}); err != nil {
		t.Fatalf("second EnsureStream (idempotent): %v", err)
	}

	stream, err := js.Stream(ctx, bus.DefaultStreamName)
	if err != nil {
		t.Fatalf("Stream lookup: %v", err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatalf("Stream info: %v", err)
	}
	if info.Config.Name != bus.DefaultStreamName {
		t.Fatalf("stream name = %q, want %q", info.Config.Name, bus.DefaultStreamName)
	}
}

func TestIntegration_EnsureObjectStoreIdempotent(t *testing.T) {
	url, stop := startNATS(t)
	defer stop()

	js, _, close := newJSClient(t, url)
	defer close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := bus.EnsureObjectStore(ctx, js, bus.DefaultObjectStoreBucket); err != nil {
		t.Fatalf("first EnsureObjectStore: %v", err)
	}
	if _, err := bus.EnsureObjectStore(ctx, js, bus.DefaultObjectStoreBucket); err != nil {
		t.Fatalf("second EnsureObjectStore (idempotent): %v", err)
	}
}

func TestIntegration_PublishInlineRoundTrip(t *testing.T) {
	url, stop := startNATS(t)
	defer stop()

	js, nc, closeJS := newJSClient(t, url)
	defer closeJS()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := bus.EnsureStream(ctx, js, bus.DefaultStreamName, []string{"gateway.>"}); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}

	received := make(chan []byte, 4)
	sub, err := nc.Subscribe("gateway.request", func(msg *nats.Msg) {
		cp := make([]byte, len(msg.Data))
		copy(cp, msg.Data)
		received <- cp
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	p := bus.New(bus.Options{
		JS:        js,
		QueueSize: 8,
		Workers:   2,
		Logger:    discardLogger(),
	})
	p.Start(ctx)
	defer p.Stop(2 * time.Second)

	payload := []byte(`{"hello":"world"}`)
	p.Publish(bus.Envelope{
		EventID:       "round-trip-1",
		EventType:     "request",
		Timestamp:     time.Now().UTC(),
		Mode:          bus.PayloadInline,
		InlinePayload: payload,
	})

	select {
	case data := <-received:
		var env bus.Envelope
		if err := msgpack.Unmarshal(data, &env); err != nil {
			t.Fatalf("decode envelope: %v", err)
		}
		if env.EventID != "round-trip-1" {
			t.Errorf("EventID = %q", env.EventID)
		}
		if string(env.InlinePayload) != string(payload) {
			t.Errorf("payload = %q, want %q", env.InlinePayload, payload)
		}
		if env.Mode != bus.PayloadInline {
			t.Errorf("Mode = %d, want PayloadInline", env.Mode)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no message received within 10s")
	}
}

func TestIntegration_PublishStashedRoundTrip(t *testing.T) {
	url, stop := startNATS(t)
	defer stop()

	js, nc, closeJS := newJSClient(t, url)
	defer closeJS()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := bus.EnsureStream(ctx, js, bus.DefaultStreamName, []string{"gateway.>"}); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}
	store, err := bus.EnsureObjectStore(ctx, js, bus.DefaultObjectStoreBucket)
	if err != nil {
		t.Fatalf("EnsureObjectStore: %v", err)
	}

	received := make(chan []byte, 4)
	sub, err := nc.Subscribe("gateway.request", func(msg *nats.Msg) {
		cp := make([]byte, len(msg.Data))
		copy(cp, msg.Data)
		received <- cp
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	threshold := 1024
	p := bus.New(bus.Options{
		JS:             js,
		ObjectStore:    store,
		QueueSize:      4,
		Workers:        1,
		StashThreshold: threshold,
		Logger:         discardLogger(),
	})
	p.Start(ctx)
	defer p.Stop(2 * time.Second)

	body := make([]byte, threshold*2)
	for i := range body {
		body[i] = byte('a' + i%26)
	}
	p.Publish(bus.Envelope{
		EventID:       "stash-1",
		EventType:     "request",
		Timestamp:     time.Now().UTC(),
		Mode:          bus.PayloadInline,
		InlinePayload: body,
	})

	select {
	case data := <-received:
		var env bus.Envelope
		if err := msgpack.Unmarshal(data, &env); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if env.Mode != bus.PayloadStashed {
			t.Fatalf("Mode = %d, want PayloadStashed", env.Mode)
		}
		if env.ObjectRef == nil {
			t.Fatal("ObjectRef nil")
		}
		if env.ObjectRef.Key != "stash-1" {
			t.Errorf("Key = %q, want stash-1", env.ObjectRef.Key)
		}

		got, err := store.GetBytes(ctx, env.ObjectRef.Key)
		if err != nil {
			t.Fatalf("fetch stash: %v", err)
		}
		if len(got) != len(body) {
			t.Fatalf("stash size = %d, want %d", len(got), len(body))
		}
		for i := range got {
			if got[i] != body[i] {
				t.Fatalf("stash byte mismatch at %d: %d vs %d", i, got[i], body[i])
			}
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no message received within 15s")
	}
}
