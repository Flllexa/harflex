// Package procattr keeps the processes the desktop app starts out of sight.
package procattr

import "os/exec"

// Hide keeps a command from opening a console window on Windows, where a desktop app that starts a console program
// (git, a CLI, PowerShell) otherwise shows one for as long as it runs. Elsewhere it changes nothing.
func Hide(cmd *exec.Cmd) { hide(cmd) }
