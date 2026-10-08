package tools

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/agentcore"
)

// The errors below name a cause the model can act on. Each is wrapped where the cause is known, and the
// wrappers of the file tools and the shell turn them into an agentcore.ToolFailure whose code and message
// are written here, never copied from the underlying error: that text carries absolute paths of the
// private execution root, and the run must neither journal nor show them. Every other error keeps ending
// the run.
var (
	errOutsideWorkspace = errors.New("path is outside the workspace")
	errNotDirectory     = errors.New("not a directory")
	errNotRegularFile   = errors.New("not a regular file")
	errRangeRequired    = errors.New("a line range is required")
	errBinaryFile       = errors.New("unsupported binary file")
)

// classifiedError keeps an existing message and adds the cause the classifier looks for.
type classifiedError struct {
	kind error
	msg  string
}

func (e *classifiedError) Error() string        { return e.msg }
func (e *classifiedError) Is(target error) bool { return target == e.kind }

func classified(kind error, format string, args ...any) error {
	return &classifiedError{kind: kind, msg: fmt.Sprintf(format, args...)}
}

// argumentError is a call whose arguments are wrong: the model can send them again, corrected.
type argumentError struct{ detail string }

func (e *argumentError) Error() string { return e.detail }

func badArguments(format string, args ...any) error {
	return &argumentError{detail: fmt.Sprintf(format, args...)}
}

// editMatchError is an edit whose oldText did not match exactly once.
type editMatchError struct{ count int }

func (e *editMatchError) Error() string {
	return fmt.Sprintf("oldText must match exactly once (found %d)", e.count)
}

// toolError wraps an error the way every file tool always did and, when the model can act on it, as a
// ToolFailure the run survives.
func toolError(name string, guard *PathGuard, err error) error {
	if failure := toolFailureFor(guard, err); failure != nil {
		return fmt.Errorf("%s: %w", name, failure.WithCause(err))
	}
	return fmt.Errorf("%s: %w", name, err)
}

func toolFailureFor(guard *PathGuard, err error) *agentcore.ToolFailure {
	var arguments *argumentError
	var match *editMatchError
	switch {
	case errors.Is(err, errOutsideWorkspace):
		// Reaching for something outside the project is not a slip the run absorbs: the sandbox did its job and
		// the run still ends, so a Coder that tries it never reports success.
		return nil
	case errors.As(err, &arguments):
		return &agentcore.ToolFailure{Code: "invalid_arguments", Message: "the arguments are not valid: " + safeDetail(arguments.detail)}
	case errors.As(err, &match):
		if match.count == 0 {
			return &agentcore.ToolFailure{Code: "no_match", Message: "oldText was not found in the file"}
		}
		return &agentcore.ToolFailure{Code: "ambiguous_match", Message: fmt.Sprintf("oldText matches %d times; it must match exactly once", match.count)}
	case errors.Is(err, errBinaryFile):
		return &agentcore.ToolFailure{Code: "binary_file", Message: "the file is binary and this tool only handles text"}
	case errors.Is(err, errRangeRequired):
		return &agentcore.ToolFailure{Code: "range_required", Message: "the file is larger than 10 MiB; read it again with offset and limit"}
	case errors.Is(err, errNotDirectory):
		return &agentcore.ToolFailure{Code: "not_a_directory", Message: "the path is not a directory"}
	case errors.Is(err, errNotRegularFile):
		return &agentcore.ToolFailure{Code: "not_a_file", Message: "the path is not a regular file"}
	case errors.Is(err, fs.ErrNotExist):
		return missingPathFailure(guard, err, "not_found", "%s does not exist", "the path does not exist")
	case errors.Is(err, fs.ErrPermission):
		return missingPathFailure(guard, err, "permission_denied", "permission denied for %s", "permission denied")
	}
	return nil
}

// missingPathFailure names the path the model asked for, relative to the project. A path that is not
// inside the project (lexically, or after following the links on the way) is never described: it ends the run
// whether or not it exists, so no answer reveals anything about the rest of the disk.
func missingPathFailure(guard *PathGuard, err error, code, format, fallback string) *agentcore.ToolFailure {
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) || guard == nil {
		return &agentcore.ToolFailure{Code: code, Message: fallback}
	}
	relative, inside := insideRoot(guard.root, pathErr.Path)
	if !inside || !resolvesInside(guard.root, pathErr.Path) {
		return nil
	}
	return &agentcore.ToolFailure{Code: code, Message: fmt.Sprintf(format, safeQuoted(relative))}
}

func insideRoot(root, path string) (string, bool) {
	if path == "" {
		return "", false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	relative, err := filepath.Rel(root, filepath.Clean(path))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}

// resolvesInside follows the links of the nearest part of path that exists and reports whether it stays
// in the project. A missing file below a link that leads out of the project is outside it too.
func resolvesInside(root, path string) bool {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	for current := filepath.Dir(filepath.Clean(path)); ; current = filepath.Dir(current) {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			_, inside := insideRoot(root, resolved)
			return inside
		}
		if current == filepath.Dir(current) {
			return false
		}
	}
}

func safeQuoted(text string) string {
	return fmt.Sprintf("%q", safeDetail(text))
}

// safeDetail trims text to one printable line of bounded size.
func safeDetail(text string) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	const limit = 200
	if len(text) > limit {
		text = text[:limit]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
		text += "…"
	}
	return text
}

// shellFailure says whether a shell call ended the ordinary way for a command that did not do what was
// hoped: it exited with a status, or ran past its own time limit. Cancellation, the run's own deadline
// and anything unexpected stay generic failures.
func shellFailure(runErr, ctxErr error, source string, details shellDetails, timeout time.Duration, encodeErr error) *agentcore.ToolFailure {
	if encodeErr != nil || runErr == nil {
		return nil
	}
	switch {
	case errors.Is(ctxErr, context.DeadlineExceeded) && source == "tool":
		// Stopping a command can report that the signal found nothing left to stop; that is not a second problem.
		if !onlyCauses(runErr, func(err error) bool {
			return isExitError(err) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.ESRCH)
		}) {
			return nil
		}
		return &agentcore.ToolFailure{Code: "timeout", Message: fmt.Sprintf("the command exceeded its time limit of %s", timeout.Round(time.Millisecond))}
	case ctxErr != nil || !onlyCauses(runErr, isExitError):
		return nil
	case details.ExitCode > 0:
		return &agentcore.ToolFailure{Code: "exit_status", Message: fmt.Sprintf("the command exited with status %d", details.ExitCode)}
	case details.ExitCode < 0:
		return &agentcore.ToolFailure{Code: "exit_status", Message: "the command ended without an exit status, stopped by a signal"}
	}
	return nil
}

func isExitError(err error) bool {
	_, ok := err.(*exec.ExitError)
	return ok
}

// onlyCauses reports whether every leaf cause in err is accepted. Joined errors and wrapped errors are
// walked; anything else (a broken pipe, a failed read, a callback that panicked) is unexpected.
func onlyCauses(err error, accept func(error) bool) bool {
	if err == nil {
		return false
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		for _, item := range wrapped.Unwrap() {
			if item != nil && !onlyCauses(item, accept) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		if accept(err) {
			return true
		}
		return onlyCauses(wrapped.Unwrap(), accept)
	}
	return accept(err)
}
