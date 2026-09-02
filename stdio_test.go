package rotini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// serve runs one session over the given input and returns everything written.
func serve(t *testing.T, framing Framing, input string, wire func(*StdioServer)) string {
	t.Helper()
	var out bytes.Buffer
	s := NewStdioServer(strings.NewReader(input), &out).WithFraming(framing)
	wire(s)
	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return out.String()
}

func decodeLine(t *testing.T, line string) rpcMessage {
	t.Helper()
	var msg rpcMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		t.Fatalf("decode %q: %v", line, err)
	}
	return msg
}

func TestStdioServer_answersRequests(t *testing.T) {
	got := serve(t, FramingLine, `{"jsonrpc":"2.0","id":1,"method":"add","params":{"a":2,"b":3}}`+"\n",
		func(s *StdioServer) {
			s.Handle("add", func(_ context.Context, params json.RawMessage) (any, error) {
				var p struct {
					A int `json:"a"`
					B int `json:"b"`
				}
				if err := json.Unmarshal(params, &p); err != nil {
					return nil, NewRPCError(CodeInvalidParams, "bad params")
				}
				return p.A + p.B, nil
			})
		})

	msg := decodeLine(t, strings.TrimSpace(got))
	if msg.JSONRPC != "2.0" || string(msg.ID) != "1" {
		t.Errorf("reply envelope wrong: %+v", msg)
	}
	if fmt.Sprint(msg.Result) != "5" {
		t.Errorf("result = %v, want 5", msg.Result)
	}
}

// A notification has no id and, by protocol, gets no reply — silence is the
// correct answer, and an unknown one is not an error.
func TestStdioServer_notificationsAreSilent(t *testing.T) {
	var seen bool
	got := serve(t, FramingLine,
		`{"jsonrpc":"2.0","method":"ping"}`+"\n"+`{"jsonrpc":"2.0","method":"unregistered"}`+"\n",
		func(s *StdioServer) {
			s.Notify("ping", func(context.Context, json.RawMessage) error { seen = true; return nil })
		})
	if !seen {
		t.Error("the notification handler did not run")
	}
	if got != "" {
		t.Errorf("a notification was answered: %q", got)
	}
}

func TestStdioServer_protocolErrors(t *testing.T) {
	cases := []struct {
		name, input string
		wantCode    int
	}{
		{"malformed JSON", "not json at all\n", CodeParseError},
		{"no method", `{"jsonrpc":"2.0","id":1}` + "\n", CodeInvalidRequest},
		{"unknown method", `{"jsonrpc":"2.0","id":1,"method":"nope"}` + "\n", CodeMethodNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := serve(t, FramingLine, tc.input, func(*StdioServer) {})
			msg := decodeLine(t, strings.TrimSpace(got))
			if msg.Error == nil || msg.Error.Code != tc.wantCode {
				t.Errorf("error = %+v, want code %d", msg.Error, tc.wantCode)
			}
		})
	}
}

// A handler's *RPCError passes through verbatim so it controls the code a peer
// sees; any other error becomes a generic internal error carrying its message.
func TestStdioServer_handlerErrorMapping(t *testing.T) {
	got := serve(t, FramingLine,
		`{"jsonrpc":"2.0","id":1,"method":"typed"}`+"\n"+`{"jsonrpc":"2.0","id":2,"method":"plain"}`+"\n",
		func(s *StdioServer) {
			s.Handle("typed", func(context.Context, json.RawMessage) (any, error) {
				return nil, NewRPCError(CodeInvalidParams, "needs a name")
			})
			s.Handle("plain", func(context.Context, json.RawMessage) (any, error) {
				return nil, errors.New("something broke")
			})
		})

	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 replies, got %d: %q", len(lines), got)
	}
	typed := decodeLine(t, lines[0])
	if typed.Error == nil || typed.Error.Code != CodeInvalidParams || typed.Error.Message != "needs a name" {
		t.Errorf("typed error not passed through: %+v", typed.Error)
	}
	plain := decodeLine(t, lines[1])
	if plain.Error == nil || plain.Error.Code != CodeInternalError || !strings.Contains(plain.Error.Message, "something broke") {
		t.Errorf("plain error not mapped to internal: %+v", plain.Error)
	}
}

// The LSP/MCP framing: Content-Length headers in both directions.
func TestStdioServer_contentLengthFraming(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":7,"method":"echo","params":"hi"}`
	input := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)

	got := serve(t, FramingContentLength, input, func(s *StdioServer) {
		s.Handle("echo", func(_ context.Context, p json.RawMessage) (any, error) {
			return json.RawMessage(p), nil
		})
	})

	header, payload, ok := strings.Cut(got, "\r\n\r\n")
	if !ok {
		t.Fatalf("reply is not Content-Length framed: %q", got)
	}
	if !strings.HasPrefix(header, "Content-Length: ") {
		t.Errorf("missing Content-Length header: %q", header)
	}
	var announced int
	fmt.Sscanf(header, "Content-Length: %d", &announced)
	if announced != len(payload) {
		t.Errorf("Content-Length %d does not match the %d-byte body", announced, len(payload))
	}
	if msg := decodeLine(t, payload); string(msg.ID) != "7" {
		t.Errorf("reply id = %s, want 7", msg.ID)
	}
}

// Several messages arrive back to back on one pipe; replies come out in order.
func TestStdioServer_multipleMessagesInOrder(t *testing.T) {
	var input strings.Builder
	for i := 1; i <= 3; i++ {
		fmt.Fprintf(&input, `{"jsonrpc":"2.0","id":%d,"method":"n"}`+"\n", i)
	}
	got := serve(t, FramingLine, input.String(), func(s *StdioServer) {
		s.Handle("n", func(context.Context, json.RawMessage) (any, error) { return "ok", nil })
	})
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 replies, got %d", len(lines))
	}
	for i, line := range lines {
		if id := string(decodeLine(t, line).ID); id != fmt.Sprint(i+1) {
			t.Errorf("reply %d has id %s, want %d — replies must stay in arrival order", i, id, i+1)
		}
	}
}

// The peer closing the pipe is how a stdio session ends: a clean stop, not an error.
func TestStdioServer_endOfInputIsCleanStop(t *testing.T) {
	if got := serve(t, FramingLine, "", func(*StdioServer) {}); got != "" {
		t.Errorf("empty session wrote %q", got)
	}
	if got := serve(t, FramingContentLength, "", func(*StdioServer) {}); got != "" {
		t.Errorf("empty content-length session wrote %q", got)
	}
}

func TestStdioServer_blankLinesIgnored(t *testing.T) {
	got := serve(t, FramingLine, "\n\n"+`{"jsonrpc":"2.0","id":1,"method":"n"}`+"\n",
		func(s *StdioServer) {
			s.Handle("n", func(context.Context, json.RawMessage) (any, error) { return true, nil })
		})
	if len(strings.Split(strings.TrimSpace(got), "\n")) != 1 {
		t.Errorf("blank lines produced replies: %q", got)
	}
}

func TestStdioServer_requiresStreams(t *testing.T) {
	if err := NewStdioServer(nil, nil).Run(context.Background()); !errors.Is(err, ErrInternal) {
		t.Errorf("Run with no streams = %v, want an internal error", err)
	}
}

func TestStdioServer_contextCancellationStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if err := NewStdioServer(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"n"}`+"\n"), &out).
		Run(ctx); err != nil {
		t.Errorf("canceled Run = %v, want nil", err)
	}
	if out.Len() != 0 {
		t.Errorf("a canceled server still answered: %q", out.String())
	}
}
