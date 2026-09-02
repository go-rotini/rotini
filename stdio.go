package rotini

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// Framing is how a [StdioServer] delimits messages on the wire.
type Framing int

const (
	// FramingLine is one JSON object per line (newline-delimited JSON-RPC), the
	// default. It is the easiest to drive from a shell and from tests.
	FramingLine Framing = iota
	// FramingContentLength prefixes each message with "Content-Length: <n>" and a
	// blank line — the framing LSP and MCP use over stdio.
	FramingContentLength
)

// JSON-RPC 2.0 error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// RPCError is a JSON-RPC error object. A handler returns one to control the code
// and data a peer sees; any other error becomes [CodeInternalError] with the
// error's message.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("jsonrpc %d: %s", e.Code, e.Message) }

// NewRPCError returns an error a handler can return to set the JSON-RPC code.
func NewRPCError(code int, message string) *RPCError {
	return &RPCError{Code: code, Message: message}
}

// RequestFunc answers a JSON-RPC request. Its return value is marshaled as the
// result; an error becomes the error object (an [*RPCError] verbatim, anything
// else as [CodeInternalError]).
type RequestFunc = func(ctx context.Context, params json.RawMessage) (any, error)

// NotifyFunc handles a JSON-RPC notification — a message with no id, which by
// protocol gets no reply. Its error is not sent anywhere; return it only so the
// server can stop cleanly on a fatal one.
type NotifyFunc = func(ctx context.Context, params json.RawMessage) error

// StdioServer runs the binary as a stdin/stdout message server: JSON-RPC 2.0
// over newline-delimited or Content-Length framing, which is how LSP language
// servers and MCP servers speak.
//
// It is the shape a CLI takes when a tool drives it instead of a human — the same
// binary, the same handlers, a different transport.
//
//	rotini.NewStdioServer(rtx.Stdin, rtx.Stdout).
//	    WithFraming(rotini.FramingContentLength).
//	    Handle("tools/list", listTools).
//	    Notify("notifications/initialized", func(context.Context, json.RawMessage) error { return nil }).
//	    Run(ctx)
//
// Requests are served ONE AT A TIME, in arrival order: a stdio peer shares one
// pipe, so concurrent handlers would interleave their writes. A handler that
// needs to do slow work should hand it to a [Service] and answer immediately.
//
// The zero value is not usable; start from [NewStdioServer].
type StdioServer struct {
	in       io.Reader
	out      io.Writer
	framing  Framing
	mu       sync.Mutex
	requests map[string]RequestFunc
	notifies map[string]NotifyFunc
}

// NewStdioServer returns a server reading messages from in and writing replies to
// out. Pass a handler's [Context.Stdin] and [Context.Stdout].
func NewStdioServer(in io.Reader, out io.Writer) *StdioServer {
	return &StdioServer{
		in:       in,
		out:      out,
		requests: map[string]RequestFunc{},
		notifies: map[string]NotifyFunc{},
	}
}

// WithFraming selects the wire framing (default [FramingLine]). It returns the
// receiver to chain.
func (s *StdioServer) WithFraming(f Framing) *StdioServer { s.framing = f; return s }

// Handle registers the handler for a request method, replacing any prior one. It
// returns the receiver to chain.
func (s *StdioServer) Handle(method string, fn RequestFunc) *StdioServer {
	if fn != nil {
		s.mu.Lock()
		s.requests[method] = fn
		s.mu.Unlock()
	}
	return s
}

// Notify registers the handler for a notification method — a message with no id,
// which gets no reply. It returns the receiver to chain.
func (s *StdioServer) Notify(method string, fn NotifyFunc) *StdioServer {
	if fn != nil {
		s.mu.Lock()
		s.notifies[method] = fn
		s.mu.Unlock()
	}
	return s
}

// rpcMessage is one JSON-RPC frame in either direction.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// Run serves messages until end of input or a done context. End of input is a
// clean stop — the peer closing the pipe is how a stdio session ends.
func (s *StdioServer) Run(ctx context.Context) error {
	if s.in == nil || s.out == nil {
		return InternalError(errors.New("rotini: stdio server has no streams"))
	}
	reader := bufio.NewReader(s.in)

	for {
		select {
		case <-ctx.Done():
			return nil // a canceled server stopped on request, not in failure
		default:
		}
		frame, err := s.read(reader)
		switch {
		case errors.Is(err, io.EOF):
			return nil // the peer closed the pipe: that is how a session ends
		case err != nil:
			return err
		}
		if strings.TrimSpace(string(frame)) == "" {
			continue
		}
		if err := s.dispatch(ctx, frame); err != nil {
			return err
		}
	}
}

// dispatch decodes one frame and answers it.
func (s *StdioServer) dispatch(ctx context.Context, frame []byte) error {
	var msg rpcMessage
	if err := json.Unmarshal(frame, &msg); err != nil {
		return s.reply(&rpcMessage{JSONRPC: "2.0", Error: NewRPCError(CodeParseError, "invalid JSON")})
	}
	if msg.Method == "" {
		return s.reply(&rpcMessage{JSONRPC: "2.0", ID: msg.ID, Error: NewRPCError(CodeInvalidRequest, "missing method")})
	}

	// No id means a notification: it is answered with silence, by protocol.
	if len(msg.ID) == 0 {
		s.mu.Lock()
		fn := s.notifies[msg.Method]
		s.mu.Unlock()
		if fn == nil {
			return nil
		}
		return fn(ctx, msg.Params)
	}

	s.mu.Lock()
	fn := s.requests[msg.Method]
	s.mu.Unlock()
	if fn == nil {
		return s.reply(&rpcMessage{JSONRPC: "2.0", ID: msg.ID,
			Error: NewRPCError(CodeMethodNotFound, "unknown method "+strconv.Quote(msg.Method))})
	}

	result, err := fn(ctx, msg.Params)
	if err != nil {
		var rpcErr *RPCError
		if !errors.As(err, &rpcErr) {
			rpcErr = NewRPCError(CodeInternalError, err.Error())
		}
		return s.reply(&rpcMessage{JSONRPC: "2.0", ID: msg.ID, Error: rpcErr})
	}
	return s.reply(&rpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: result})
}

// reply marshals and frames one outgoing message.
func (s *StdioServer) reply(msg *rpcMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return InternalError(fmt.Errorf("rotini: encode reply: %w", err))
	}
	if s.framing == FramingContentLength {
		if _, err := fmt.Fprintf(s.out, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
			return fmt.Errorf("rotini: write reply: %w", err)
		}
		return nil
	}
	if _, err := s.out.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("rotini: write reply: %w", err)
	}
	return nil
}

// read pulls one framed message off the wire.
func (s *StdioServer) read(r *bufio.Reader) ([]byte, error) {
	if s.framing == FramingContentLength {
		return readContentLength(r)
	}
	line, err := r.ReadBytes('\n')
	switch {
	case err == nil:
		return line, nil
	case errors.Is(err, io.EOF) && len(line) > 0:
		return line, nil // a final line with no trailing newline is still a message
	case errors.Is(err, io.EOF):
		return nil, io.EOF
	default:
		return nil, fmt.Errorf("rotini: read message: %w", err)
	}
}

// readContentLength reads the LSP/MCP header block and the body it announces.
func readContentLength(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("rotini: read header: %w", err)
		}
		header := strings.TrimRight(line, "\r\n")
		if header == "" {
			break // the blank line ends the header block
		}
		name, value, ok := strings.Cut(header, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "content-length") {
			n, convErr := strconv.Atoi(strings.TrimSpace(value))
			if convErr != nil {
				return nil, UsageError(fmt.Errorf("rotini: bad Content-Length %q", strings.TrimSpace(value)))
			}
			length = n
		}
	}
	if length < 0 {
		return nil, UsageError(errors.New("rotini: message has no Content-Length header"))
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, fmt.Errorf("rotini: read body: %w", err)
	}
	return body, nil
}
