package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/security"
)

const shellTruncation = "\n[output truncated]\n"

var ErrShellCallback = errors.New("shell update callback panicked")

type ShellConfig struct {
	CWD            string
	MaxOutputBytes int
	DefaultTimeout time.Duration
	// Env is the complete command environment. Nil selects the allowlisted
	// system variables of the harness process; nothing else is inherited.
	Env []string
}

type shellTool struct{ config ShellConfig }

func NewShellTool(config ShellConfig) Tool {
	if config.MaxOutputBytes <= 0 {
		config.MaxOutputBytes = 1024 * 1024
	}
	if config.DefaultTimeout <= 0 {
		config.DefaultTimeout = 2 * time.Minute
	}
	return shellTool{config: config}
}

func (t shellTool) Spec() agentcore.ToolSpec {
	return agentcore.ToolSpec{
		Name:        shellToolName,
		Description: "Run a shell command in the configured working directory with bounded streaming output and cancellation.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","minLength":1},"timeoutMs":{"type":"integer","minimum":1,"maximum":9223372036854}},"required":["command"],"additionalProperties":false}`),
	}
}

func (t shellTool) Risk() security.Risk { return security.Shell }

type shellDetails struct {
	ExitCode      int    `json:"exitCode"`
	TimedOut      bool   `json:"timedOut"`
	Cancelled     bool   `json:"cancelled"`
	Truncated     bool   `json:"truncated"`
	DurationMs    int64  `json:"durationMs"`
	StdoutBytes   int64  `json:"stdoutBytes"`
	StderrBytes   int64  `json:"stderrBytes"`
	TimeoutSource string `json:"timeoutSource,omitempty"`
}

func (t shellTool) Execute(parent context.Context, args json.RawMessage, update agentcore.ToolUpdateSink) (agentcore.ToolExecutionResult, error) {
	result, err := t.execute(parent, args, update)
	var arguments *argumentError
	if errors.As(err, &arguments) {
		failure := &agentcore.ToolFailure{Code: "invalid_arguments", Message: "the arguments are not valid: " + safeDetail(arguments.detail)}
		return result, fmt.Errorf("%s: %w", shellToolName, failure.WithCause(err))
	}
	return result, err
}

func (t shellTool) execute(parent context.Context, args json.RawMessage, update agentcore.ToolUpdateSink) (agentcore.ToolExecutionResult, error) {
	var params struct {
		Command   string          `json:"command"`
		TimeoutMs json.RawMessage `json:"timeoutMs"`
	}
	if err := decodeReadOnly(args, &params); err != nil {
		return agentcore.ToolExecutionResult{}, fmt.Errorf("shell: %w", err)
	}
	if strings.TrimSpace(params.Command) == "" {
		return agentcore.ToolExecutionResult{}, badArguments("command is required")
	}
	timeout := t.config.DefaultTimeout
	if len(params.TimeoutMs) != 0 {
		var ms int64
		if err := json.Unmarshal(params.TimeoutMs, &ms); err != nil {
			return agentcore.ToolExecutionResult{}, badArguments("decode timeoutMs: %v", err)
		}
		if ms <= 0 || ms > math.MaxInt64/int64(time.Millisecond) {
			return agentcore.ToolExecutionResult{}, badArguments("timeoutMs must be a positive duration in range")
		}
		timeout = time.Duration(ms) * time.Millisecond
	}
	if t.config.CWD == "" {
		return agentcore.ToolExecutionResult{}, fmt.Errorf("shell: working directory is required")
	}
	cwd, err := filepath.Abs(t.config.CWD)
	if err != nil {
		return agentcore.ToolExecutionResult{}, fmt.Errorf("shell: resolve working directory: %w", err)
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return agentcore.ToolExecutionResult{}, fmt.Errorf("shell: stat working directory: %w", err)
	}
	if !info.IsDir() {
		return agentcore.ToolExecutionResult{}, fmt.Errorf("shell: working directory %q is not a directory", cwd)
	}

	started := time.Now()
	ctx := parent
	source := "parent"
	deadline := started.Add(timeout)
	if parentDeadline, ok := parent.Deadline(); !ok || deadline.Before(parentDeadline) {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(parent, deadline)
		defer cancel()
		source = "tool"
	}
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	collector := &shellCollector{remaining: t.config.MaxOutputBytes}
	details := shellDetails{ExitCode: -1}
	finish := func(runErr error) (agentcore.ToolExecutionResult, error) {
		details.DurationMs = time.Since(started).Milliseconds()
		details.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
		details.Cancelled = errors.Is(ctx.Err(), context.Canceled)
		if details.TimedOut {
			details.TimeoutSource = source
		}
		details.StdoutBytes, details.StderrBytes = collector.stdoutBytes, collector.stderrBytes
		details.Truncated = collector.truncated
		result, encodeErr := textResult(collector.text(), details)
		if err := errors.Join(runErr, ctx.Err(), encodeErr); err != nil {
			// A command that failed or ran out of time is something the model can act on; the output stays with it.
			if failure := shellFailure(runErr, ctx.Err(), source, details, timeout, encodeErr); failure != nil {
				return result, fmt.Errorf("shell execution: %w", failure.WithCause(err))
			}
			return result, fmt.Errorf("shell execution: %w", err)
		}
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		return finish(err)
	}
	command, err := newShellCommand(params.Command)
	if err != nil {
		return finish(err)
	}
	defer command.close()
	cmd := command.cmd
	cmd.Dir = cwd
	cmd.Env = t.config.Env
	if cmd.Env == nil {
		cmd.Env = shellEnvironment(os.Environ(), runtime.GOOS)
	}
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		return finish(fmt.Errorf("open stdout pipe: %w", err))
	}
	defer stdout.Close()
	defer stdoutWriter.Close()
	stderr, stderrWriter, err := os.Pipe()
	if err != nil {
		return finish(fmt.Errorf("open stderr pipe: %w", err))
	}
	defer stderr.Close()
	defer stderrWriter.Close()
	cmd.Stdout, cmd.Stderr = stdoutWriter, stderrWriter
	if err := command.start(); err != nil {
		return finish(fmt.Errorf("start shell: %w", err))
	}
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	// One owned dispatcher serializes callbacks outside the collector mutex. The
	// bounded queue applies backpressure; a blocking sink must obey Tool's contract.
	updates := make(chan agentcore.ToolUpdate, 8)
	dispatched := make(chan struct{})
	var callbackErr error
	collector.emit = func(value agentcore.ToolUpdate) {
		select {
		case updates <- value:
		case <-runCtx.Done():
		}
	}
	go func() {
		defer close(dispatched)
		for value := range updates {
			if callbackErr == nil && update != nil {
				callbackErr = invokeShellUpdate(runCtx, update, value)
				if callbackErr != nil {
					cancelRun()
				}
			}
		}
	}()
	var readers sync.WaitGroup
	var readErrors [2]error
	for i, stream := range []struct {
		name string
		pipe *os.File
	}{{"stdout", stdout}, {"stderr", stderr}} {
		readers.Add(1)
		go func() {
			defer readers.Done()
			writer := &shellStreamWriter{collector: collector, stream: stream.name}
			_, readErrors[i] = io.Copy(writer, stream.pipe)
			writer.flush()
		}()
	}
	waitErr := command.wait(runCtx)
	waitErr = errors.Join(waitErr, command.close())
	// Pipes are owned here, so Wait cannot close them before buffered output is drained.
	joined := make(chan struct{})
	go func() { readers.Wait(); close(joined) }()
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-joined:
	case <-timer.C:
		// A detached descendant must not keep an invocation's readers alive forever.
		_ = stdout.Close()
		_ = stderr.Close()
		<-joined
	}
	close(updates)
	<-dispatched
	waitErr = errors.Join(waitErr, callbackErr)
	if cmd.ProcessState != nil {
		details.ExitCode = cmd.ProcessState.ExitCode()
	}
	for _, readErr := range readErrors {
		if readErr != nil && !errors.Is(readErr, os.ErrClosed) {
			waitErr = errors.Join(waitErr, fmt.Errorf("read shell output: %w", readErr))
		}
	}
	return finish(waitErr)
}

type shellCollector struct {
	mu                       sync.Mutex
	remaining                int
	stdout, stderr           strings.Builder
	stdoutBytes, stderrBytes int64
	truncated                bool
	emit                     func(agentcore.ToolUpdate)
}

type shellStreamWriter struct {
	collector *shellCollector
	stream    string
	carry     []byte
}

func (w *shellStreamWriter) Write(data []byte) (int, error) {
	w.collect(w.decode(data, false), len(data))
	return len(data), nil
}

func (w *shellStreamWriter) flush() { w.collect(w.decode(nil, true), 0) }

func (w *shellStreamWriter) decode(data []byte, eof bool) string {
	data = append(w.carry, data...)
	var text strings.Builder
	for len(data) > 0 {
		if !eof && !utf8.FullRune(data) {
			break
		}
		r, size := utf8.DecodeRune(data)
		text.WriteRune(r)
		data = data[size:]
	}
	w.carry = append(w.carry[:0], data...)
	return text.String()
}

func (w *shellStreamWriter) collect(text string, rawBytes int) {
	c := w.collector
	c.mu.Lock()
	defer c.mu.Unlock()
	if w.stream == "stdout" {
		c.stdoutBytes += int64(rawBytes)
	} else {
		c.stderrBytes += int64(rawBytes)
	}
	n := min(len(text), c.remaining)
	for n > 0 && n < len(text) && !utf8.RuneStart(text[n]) {
		n--
	}
	if c.truncated {
		n = 0
	}
	if n > 0 {
		if w.stream == "stdout" {
			c.stdout.WriteString(text[:n])
		} else {
			c.stderr.WriteString(text[:n])
		}
		c.remaining -= n
		if c.emit != nil {
			c.emit(agentcore.ToolUpdate{Stream: w.stream, Text: text[:n]})
		}
	}
	if n < len(text) && !c.truncated {
		c.truncated = true
		if c.emit != nil {
			c.emit(agentcore.ToolUpdate{Stream: "system", Text: shellTruncation})
		}
	}
}

func (c *shellCollector) text() string {
	text := c.stdout.String()
	if c.stderr.Len() > 0 {
		text += "\n[stderr]\n" + c.stderr.String()
	}
	if c.truncated {
		text += shellTruncation
	}
	return text
}

func invokeShellUpdate(ctx context.Context, update agentcore.ToolUpdateSink, value agentcore.ToolUpdate) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrShellCallback
		}
	}()
	update(ctx, value)
	return nil
}
