package tools

import "strings"

// Commands never inherit the harness environment: only variables a fresh
// terminal would also have survive. The Unix login shell re-reads the user's
// own profile, so project tooling configured there still works.
var shellEnvironmentNames = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TERM", "TMPDIR", "LANG", "LC_ALL", "LC_CTYPE", "SSH_AUTH_SOCK",
	"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR",
	"SystemRoot", "WINDIR", "ComSpec", "PATHEXT", "USERPROFILE", "TEMP", "TMP", "APPDATA", "LOCALAPPDATA",
	"ProgramData", "ProgramFiles", "ProgramFiles(x86)", "CommonProgramFiles", "HOMEDRIVE", "HOMEPATH",
	"USERNAME", "USERDOMAIN", "COMPUTERNAME", "OS", "PROCESSOR_ARCHITECTURE", "NUMBER_OF_PROCESSORS", "PSModulePath",
}

func shellEnvironment(source []string, goos string) []string {
	canonical := func(name string) string {
		if goos == "windows" {
			return strings.ToUpper(name)
		}
		return name
	}
	allowed := make(map[string]bool, len(shellEnvironmentNames))
	for _, name := range shellEnvironmentNames {
		allowed[canonical(name)] = true
	}
	env := make([]string, 0, len(allowed))
	positions := make(map[string]int)
	for _, entry := range source {
		name, _, ok := strings.Cut(entry, "=")
		key := canonical(name)
		if !ok || !allowed[key] {
			continue
		}
		if i, exists := positions[key]; exists {
			env[i] = entry
		} else {
			positions[key] = len(env)
			env = append(env, entry)
		}
	}
	return env
}
