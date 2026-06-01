package claude

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/shindakun/agent-sdk-go/internal/protocol"
	"github.com/shindakun/agent-sdk-go/internal/transport"
)

// transportFactory builds a transport from a config. It is a package var so
// tests can substitute a scripted transport.
var transportFactory = func(cfg transport.Config) transport.Transport {
	return transport.New(cfg)
}

// session owns a CLI subprocess and its control-protocol engine. It is the
// shared core behind Query and Client.
type session struct {
	opts   *Options
	t      transport.Transport
	engine *protocol.Engine
	// sessionID is the CLI-assigned session id, learned from the system/init
	// message. Until then prompts use the "default" session id, matching the
	// official SDKs.
	sessionID string
}

func newSession(opts *Options) *session {
	return &session{opts: opts}
}

// connect spawns the CLI, starts the protocol engine, and performs the
// initialize handshake. handler services inbound control requests and may be
// nil.
func (s *session) connect(ctx context.Context, handler protocol.InboundHandler) error {
	args, err := s.opts.buildArgs()
	if err != nil {
		return err
	}

	s.t = transportFactory(transport.Config{
		CLIPath: s.opts.cliPath,
		Args:    args,
		Cwd:     s.opts.cwd,
		Env:     s.opts.env,
		Stderr:  s.opts.stderr,
	})

	if err := s.t.Connect(ctx); err != nil {
		return mapTransportError(err)
	}

	s.engine = protocol.NewEngine(s.t, handler)
	s.engine.Start()

	init, err := s.opts.buildInitializeRequest()
	if err != nil {
		return err
	}
	if _, err := s.engine.Initialize(ctx, init, 0); err != nil {
		_ = s.t.Close()
		return &ConnectionError{Err: err}
	}
	return nil
}

// userInput is the stream-json envelope for a user prompt sent over stdin. The
// field set mirrors the official SDKs exactly: a nested message with a string
// content, an explicit null parent_tool_use_id, and a session_id (defaulting to
// "default") so the CLI routes the turn correctly.
type userInput struct {
	Type            string           `json:"type"` // "user"
	Message         userInputMessage `json:"message"`
	ParentToolUseID *string          `json:"parent_tool_use_id"`
	SessionID       string           `json:"session_id"`
}

type userInputMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// sendPrompt writes a user prompt to the CLI over stdin.
func (s *session) sendPrompt(ctx context.Context, prompt string) error {
	sid := s.sessionID
	if sid == "" {
		sid = "default"
	}
	in := userInput{
		Type:            "user",
		Message:         userInputMessage{Role: "user", Content: prompt},
		ParentToolUseID: nil,
		SessionID:       sid,
	}
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return s.t.Write(ctx, b)
}

// engineRef exposes the protocol engine for control methods.
func (s *session) engineRef() *protocol.Engine { return s.engine }

// messages returns the raw message-line channel from the engine.
func (s *session) messages() <-chan protocol.MessageLine {
	return s.engine.Messages()
}

// close shuts the engine and transport down.
func (s *session) close() error {
	if s.engine != nil {
		s.engine.Close()
	}
	if s.t != nil {
		return mapTransportError(s.t.Close())
	}
	return nil
}

// mapTransportError translates internal transport errors into the public typed
// errors.
func mapTransportError(err error) error {
	if err == nil {
		return nil
	}
	var cliErr *transport.CLINotFoundError
	if errors.As(err, &cliErr) {
		return &CLINotFoundError{Path: cliErr.Path, Hint: cliErr.Hint}
	}
	var procErr *transport.ProcessError
	if errors.As(err, &procErr) {
		return &ProcessError{ExitCode: procErr.ExitCode, Stderr: procErr.Stderr}
	}
	return err
}

// decodeMessageLine converts a protocol message line into a public Message,
// translating EOF and non-message frames. It returns (nil, io.EOF) at stream
// end, (nil, nil) for non-message frames that should be skipped, or a decoded
// Message.
func decodeMessageLine(line protocol.MessageLine) (Message, error) {
	if line.Err != nil {
		if errors.Is(line.Err, io.EOF) {
			return nil, io.EOF
		}
		return nil, &ConnectionError{Err: line.Err}
	}
	msg, err := UnmarshalMessage(line.Data)
	if err != nil {
		if IsNotAMessage(err) {
			return nil, nil
		}
		return nil, err
	}
	return msg, nil
}
