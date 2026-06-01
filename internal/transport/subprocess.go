package transport

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

// baseArgs are the fixed flags every invocation uses: stream-json in both
// directions plus verbose so the CLI emits the full event stream.
var baseArgs = []string{
	"--output-format", "stream-json",
	"--input-format", "stream-json",
	"--verbose",
}

// stderrCaptureLimit bounds the in-memory stderr ring buffer used to enrich a
// ProcessError on non-zero exit.
const stderrCaptureLimit = 16 << 10 // 16 KiB

// subprocessTransport drives the `claude` CLI over stdio.
type subprocessTransport struct {
	cfg Config

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	readCh chan RawLine

	writeMu sync.Mutex

	wg sync.WaitGroup

	stderrMu  sync.Mutex
	stderrBuf []byte

	closeOnce sync.Once
	closeErr  error
}

// New creates a subprocess transport from cfg.
func New(cfg Config) Transport {
	return &subprocessTransport{
		cfg:    cfg,
		readCh: make(chan RawLine, 64),
	}
}

func (t *subprocessTransport) Connect(ctx context.Context) error {
	cliPath, err := discoverCLI(t.cfg.CLIPath)
	if err != nil {
		return err
	}

	args := append(append([]string(nil), baseArgs...), t.cfg.Args...)
	cmd := exec.CommandContext(ctx, cliPath, args...)
	cmd.Dir = t.cfg.Cwd
	cmd.Env = t.buildEnv()
	if t.cfg.UID != nil {
		if err := applyCredential(cmd, *t.cfg.UID, t.cfg.GID); err != nil {
			return err
		}
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	t.cmd = cmd
	t.stdin = stdin
	t.stdout = stdout
	t.stderr = stderr

	t.wg.Add(2)
	go t.readPump()
	go t.stderrPump()

	return nil
}

func (t *subprocessTransport) buildEnv() []string {
	env := os.Environ()
	// Mark the entrypoint so the CLI knows it is driven by an SDK.
	merged := map[string]string{"CLAUDE_CODE_ENTRYPOINT": "sdk-go"}
	if t.cfg.SDKVersion != "" {
		merged["CLAUDE_AGENT_SDK_VERSION"] = t.cfg.SDKVersion
	}
	if t.cfg.Cwd != "" {
		merged["PWD"] = t.cfg.Cwd
	}
	if t.cfg.EnableFileCheckpointing {
		merged["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "true"
	}
	for k, v := range t.cfg.Env {
		merged[k] = v
	}
	// Override or append.
	out := make([]string, 0, len(env)+len(merged))
	seen := map[string]bool{}
	for k, v := range merged {
		out = append(out, k+"="+v)
		seen[k] = true
	}
	for _, kv := range env {
		key := kv
		if i := indexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		if !seen[key] {
			out = append(out, kv)
		}
	}
	return out
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// readPump reads newline-delimited frames from stdout. It uses ReadBytes rather
// than bufio.Scanner so single frames are not capped at the scanner's default
// token size; stream-json lines (full messages, large tool results) can be very
// large.
func (t *subprocessTransport) readPump() {
	defer t.wg.Done()
	defer close(t.readCh)

	maxBuf := t.cfg.MaxBufferSize
	if maxBuf <= 0 {
		maxBuf = DefaultMaxBufferSize
	}

	r := bufio.NewReader(t.stdout)
	var buf []byte
	for {
		chunk, err := r.ReadBytes('\n')
		if len(chunk) > 0 {
			buf = append(buf, chunk...)
			if len(buf) > maxBuf {
				t.readCh <- RawLine{Err: fmt.Errorf("stream-json line exceeded max buffer size of %d bytes", maxBuf)}
				t.readCh <- RawLine{Err: io.EOF}
				return
			}
			if chunk[len(chunk)-1] == '\n' {
				line := trimNewline(buf)
				buf = nil
				if len(line) > 0 {
					t.readCh <- RawLine{Data: line}
				}
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.readCh <- RawLine{Err: err}
			}
			// Flush any trailing partial line without a newline.
			if len(buf) > 0 {
				t.readCh <- RawLine{Data: buf}
			}
			t.readCh <- RawLine{Err: io.EOF}
			return
		}
	}
}

func trimNewline(b []byte) []byte {
	n := len(b)
	for n > 0 && (b[n-1] == '\n' || b[n-1] == '\r') {
		n--
	}
	out := make([]byte, n)
	copy(out, b[:n])
	return out
}

func (t *subprocessTransport) stderrPump() {
	defer t.wg.Done()
	r := bufio.NewReader(t.stderr)
	chunk := make([]byte, 4096)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			t.recordStderr(chunk[:n])
			if t.cfg.Stderr != nil {
				_, _ = t.cfg.Stderr.Write(chunk[:n])
			}
		}
		if err != nil {
			return
		}
	}
}

func (t *subprocessTransport) recordStderr(p []byte) {
	t.stderrMu.Lock()
	defer t.stderrMu.Unlock()
	t.stderrBuf = append(t.stderrBuf, p...)
	if len(t.stderrBuf) > stderrCaptureLimit {
		t.stderrBuf = t.stderrBuf[len(t.stderrBuf)-stderrCaptureLimit:]
	}
}

func (t *subprocessTransport) capturedStderr() string {
	t.stderrMu.Lock()
	defer t.stderrMu.Unlock()
	return string(t.stderrBuf)
}

func (t *subprocessTransport) Write(ctx context.Context, obj []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	if t.stdin == nil {
		return errors.New("transport: not connected")
	}
	if _, err := t.stdin.Write(obj); err != nil {
		return err
	}
	if len(obj) == 0 || obj[len(obj)-1] != '\n' {
		if _, err := t.stdin.Write([]byte{'\n'}); err != nil {
			return err
		}
	}
	return nil
}

func (t *subprocessTransport) EndInput() error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if t.stdin == nil {
		return nil
	}
	err := t.stdin.Close()
	t.stdin = nil
	return err
}

func (t *subprocessTransport) Read() <-chan RawLine {
	return t.readCh
}

func (t *subprocessTransport) Close() error {
	t.closeOnce.Do(func() {
		// Closing stdin signals the CLI to finish the current turn and exit.
		// EndInput may have already closed it; guard with the write lock.
		t.writeMu.Lock()
		if t.stdin != nil {
			_ = t.stdin.Close()
			t.stdin = nil
		}
		t.writeMu.Unlock()

		// Wait for the read/stderr pumps to drain.
		t.wg.Wait()

		if t.cmd != nil {
			err := t.cmd.Wait()
			if err != nil {
				var exitErr *exec.ExitError
				if errors.As(err, &exitErr) {
					t.closeErr = &ProcessError{
						ExitCode: exitErr.ExitCode(),
						Stderr:   t.capturedStderr(),
					}
				} else {
					t.closeErr = err
				}
			}
		}
	})
	return t.closeErr
}
