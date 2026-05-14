package pipeline_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/andyjmorgan/sluice-gateway/internal/pipeline"
)

func TestMessage_AllConcreteTypesImplementMessage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		msg  pipeline.Message
	}{
		{
			name: "ResponseInitial",
			msg: pipeline.ResponseInitial{
				StatusCode: http.StatusOK,
				Headers:    http.Header{"X-Test": []string{"1"}},
				Streaming:  true,
			},
		},
		{
			name: "StreamChunk",
			msg: pipeline.StreamChunk{
				Data:  []byte("payload"),
				Event: "message",
				ID:    "evt-1",
				IsRaw: false,
			},
		},
		{
			name: "JSONResponse",
			msg:  pipeline.JSONResponse{Body: []byte(`{"ok":true}`)},
		},
		{
			name: "Error",
			msg: pipeline.Error{
				StatusCode: http.StatusBadGateway,
				Err:        errors.New("upstream blew up"),
			},
		},
		{
			name: "Complete",
			msg:  pipeline.Complete{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.msg == nil {
				t.Fatalf("nil Message for %s", tc.name)
			}
		})
	}
}

func TestMessage_RoundTripsThroughChannel(t *testing.T) {
	t.Parallel()

	want := []pipeline.Message{
		pipeline.ResponseInitial{StatusCode: 200, Headers: http.Header{}, Streaming: false},
		pipeline.JSONResponse{Body: []byte(`{}`)},
		pipeline.Complete{},
	}

	ch := make(chan pipeline.Message, len(want))
	for _, m := range want {
		ch <- m
	}
	close(ch)

	got := make([]pipeline.Message, 0, len(want))
	for m := range ch {
		got = append(got, m)
	}

	if len(got) != len(want) {
		t.Fatalf("got %d messages, want %d", len(got), len(want))
	}

	if _, ok := got[0].(pipeline.ResponseInitial); !ok {
		t.Fatalf("got[0] = %T, want ResponseInitial", got[0])
	}
	if _, ok := got[1].(pipeline.JSONResponse); !ok {
		t.Fatalf("got[1] = %T, want JSONResponse", got[1])
	}
	if _, ok := got[2].(pipeline.Complete); !ok {
		t.Fatalf("got[2] = %T, want Complete", got[2])
	}
}

func TestMessage_TypeSwitchDispatches(t *testing.T) {
	t.Parallel()

	msgs := []pipeline.Message{
		pipeline.ResponseInitial{StatusCode: 200, Headers: http.Header{}, Streaming: true},
		pipeline.StreamChunk{Data: []byte("d"), Event: "e", ID: "i"},
		pipeline.JSONResponse{Body: []byte("{}")},
		pipeline.Error{StatusCode: 500, Err: errors.New("boom")},
		pipeline.Complete{},
	}

	seen := map[string]bool{}
	for _, m := range msgs {
		switch m.(type) {
		case pipeline.ResponseInitial:
			seen["ri"] = true
		case pipeline.StreamChunk:
			seen["sc"] = true
		case pipeline.JSONResponse:
			seen["jr"] = true
		case pipeline.Error:
			seen["er"] = true
		case pipeline.Complete:
			seen["cp"] = true
		default:
			t.Fatalf("unexpected type %T", m)
		}
	}

	for _, k := range []string{"ri", "sc", "jr", "er", "cp"} {
		if !seen[k] {
			t.Fatalf("type switch did not dispatch %s", k)
		}
	}
}
