// Package terminal runs the person's own shell in a project folder behind a pseudo-terminal, for the terminal panel.
// It is an ordinary interactive shell under the person's account: nothing here is an agent or a sandbox.
package terminal

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/creack/pty"
)

var ErrNotFound = errors.New("terminal not found")
var ErrUnsupported = errors.New("terminal unsupported on this system")

// Output is one chunk of what the shell printed, base64 encoded so a multi-byte character split between chunks
// survives the trip; Exited marks the end, with the exit code.
type Output struct {
	ID     string `json:"id"`
	Data   string `json:"data,omitempty"`
	Exited bool   `json:"exited,omitempty"`
	Code   int    `json:"code,omitempty"`
}

type session struct {
	cmd  *exec.Cmd
	ptmx *os.File
	once sync.Once
}

// Manager owns the running terminals and sends their output through emit.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*session
	emit     func(Output)
}

func NewManager(emit func(Output)) *Manager {
	return &Manager{sessions: map[string]*session{}, emit: emit}
}

// Shell is the person's login shell, or a sensible default.
func Shell() string {
	if shell := os.Getenv("SHELL"); shell != "" && filepath.IsAbs(shell) {
		return shell
	}
	if runtime.GOOS == "windows" {
		return "powershell.exe"
	}
	if _, err := os.Stat("/bin/zsh"); err == nil {
		return "/bin/zsh"
	}
	return "/bin/sh"
}

// Start opens a shell in dir with the given size and returns its id.
func (m *Manager) Start(dir string, cols, rows int) (string, error) {
	if runtime.GOOS == "windows" {
		return "", ErrUnsupported
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", errors.New("terminal folder unavailable")
	}
	shell := Shell()
	cmd := exec.Command(shell, "-l")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=Harflex")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: clamp(cols, 20, 500), Rows: clamp(rows, 5, 300)})
	if err != nil {
		return "", err
	}
	id := newID()
	s := &session{cmd: cmd, ptmx: ptmx}
	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()
	go m.pump(id, s)
	return id, nil
}

func (m *Manager) pump(id string, s *session) {
	buffer := make([]byte, 32*1024)
	for {
		n, err := s.ptmx.Read(buffer)
		if n > 0 && m.emit != nil {
			m.emit(Output{ID: id, Data: base64.StdEncoding.EncodeToString(buffer[:n])})
		}
		if err != nil {
			// EOF or a closed terminal: the shell is gone.
			break
		}
	}
	code := 0
	if err := s.cmd.Wait(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else {
			code = -1
		}
	}
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	s.close()
	if m.emit != nil {
		m.emit(Output{ID: id, Exited: true, Code: code})
	}
}

func (s *session) close() {
	s.once.Do(func() { _ = s.ptmx.Close() })
}

func (m *Manager) get(id string) (*session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, ErrNotFound
	}
	return s, nil
}

// Write types into the shell.
func (m *Manager) Write(id, data string) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	_, err = io.WriteString(s.ptmx, data)
	return err
}

// Resize follows the panel's size.
func (m *Manager) Resize(id string, cols, rows int) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	return pty.Setsize(s.ptmx, &pty.Winsize{Cols: clamp(cols, 20, 500), Rows: clamp(rows, 5, 300)})
}

// Close ends the shell (hang-up, then the pseudo-terminal closes).
func (m *Manager) Close(id string) error {
	s, err := m.get(id)
	if err != nil {
		return err
	}
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(os.Interrupt)
		_ = s.cmd.Process.Kill()
	}
	s.close()
	return nil
}

// CloseAll ends every shell, when the app quits.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		_ = m.Close(id)
	}
}

func clamp(value, low, high int) uint16 {
	if value < low {
		value = low
	}
	if value > high {
		value = high
	}
	return uint16(value)
}

func newID() string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return "term-" + hex.EncodeToString(raw[:])
}
