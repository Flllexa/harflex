package repositories

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ProcessUse names one running program that sits inside a working tree: its working directory
// is in the folder, or the folder appears in the path or arguments it was started with.
type ProcessUse struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
}

type processEntry struct {
	pid, ppid int
	name      string // short command name
	cwd       string // working directory, when known
	text      string // executable path and arguments, when known
}

// ProcessesUsing says, for each folder, which running processes use it. Removing a folder under a
// terminal, an editor, a dev server or an agent breaks them even when Git holds all of its content, so
// a folder in use is not deleted. The check needs lsof (macOS, BSD) or /proc (Linux); where it cannot
// run, nothing is reported rather than guessed.
func ProcessesUsing(ctx context.Context, folders []string) map[string][]ProcessUse {
	if len(folders) == 0 {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return matchProcesses(listProcesses(callCtx), folders, os.Getpid(), ownProgramName())
}

// ownProgramName is how this program shows up in the process table, which is also how a child of ours
// looks in the instant between fork and exec.
func ownProgramName() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Base(exe)
	}
	return filepath.Base(os.Args[0])
}

func listProcesses(ctx context.Context) []processEntry {
	switch runtime.GOOS {
	case "linux":
		return listProcessesProc()
	case "windows", "plan9", "js", "wasip1":
		return nil
	default:
		return listProcessesLsof(ctx)
	}
}

func listProcessesProc() []processEntry {
	dirs, err := filepath.Glob("/proc/[0-9]*")
	if err != nil {
		return nil
	}
	entries := make([]processEntry, 0, len(dirs))
	for _, dir := range dirs {
		pid, err := strconv.Atoi(filepath.Base(dir))
		if err != nil {
			continue
		}
		entry := processEntry{pid: pid}
		entry.cwd, _ = os.Readlink(filepath.Join(dir, "cwd"))
		if cmdline, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
			entry.text = strings.TrimSpace(strings.ReplaceAll(string(cmdline), "\x00", " "))
		}
		if exe, err := os.Readlink(filepath.Join(dir, "exe")); err == nil {
			entry.text = strings.TrimSpace(exe + " " + entry.text)
		}
		if status, err := os.ReadFile(filepath.Join(dir, "status")); err == nil {
			for _, line := range strings.Split(string(status), "\n") {
				switch {
				case strings.HasPrefix(line, "Name:"):
					entry.name = strings.TrimSpace(strings.TrimPrefix(line, "Name:"))
				case strings.HasPrefix(line, "PPid:"):
					entry.ppid, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "PPid:")))
				}
			}
		}
		if entry.cwd != "" || entry.text != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

func listProcessesLsof(ctx context.Context) []processEntry {
	// Both run at once: lsof is the slow one, and ps fills in what lsof cannot see (the program a
	// process was started from).
	type result struct{ entries []processEntry }
	cwds := make(chan result, 1)
	go func() {
		out, err := exec.CommandContext(ctx, "lsof", "-w", "-n", "-P", "+c", "0", "-d", "cwd", "-F", "pcRn").Output()
		if err != nil && len(out) == 0 {
			cwds <- result{}
			return
		}
		cwds <- result{parseLsofCwd(string(out))}
	}()
	var texts []processEntry
	if out, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,command=").Output(); err == nil {
		texts = parsePs(string(out))
	}
	return mergeProcesses((<-cwds).entries, texts)
}

// parseLsofCwd reads `lsof -F pcRn`: one p record per process, then its command, parent and cwd name.
func parseLsofCwd(output string) []processEntry {
	var entries []processEntry
	var current *processEntry
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 2 {
			continue
		}
		field, value := line[0], line[1:]
		switch field {
		case 'p':
			pid, err := strconv.Atoi(value)
			if err != nil {
				current = nil
				continue
			}
			entries = append(entries, processEntry{pid: pid})
			current = &entries[len(entries)-1]
		case 'c':
			if current != nil {
				current.name = value
			}
		case 'R':
			if current != nil {
				current.ppid, _ = strconv.Atoi(value)
			}
		case 'n':
			if current != nil && current.cwd == "" {
				current.cwd = value
			}
		}
	}
	return entries
}

var psLine = regexp.MustCompile(`^\s*(\d+)\s+(\d+)\s+(.*\S)\s*$`)

func parsePs(output string) []processEntry {
	var entries []processEntry
	for _, line := range strings.Split(output, "\n") {
		match := psLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		pid, _ := strconv.Atoi(match[1])
		ppid, _ := strconv.Atoi(match[2])
		text := match[3]
		name := text
		if space := strings.IndexByte(text, ' '); space > 0 {
			name = text[:space]
		}
		entries = append(entries, processEntry{pid: pid, ppid: ppid, name: filepath.Base(name), text: text})
	}
	return entries
}

func mergeProcesses(withCwd, withText []processEntry) []processEntry {
	byPID := make(map[int]*processEntry, len(withCwd)+len(withText))
	order := make([]int, 0, len(withCwd)+len(withText))
	for _, group := range [][]processEntry{withCwd, withText} {
		for _, entry := range group {
			existing, ok := byPID[entry.pid]
			if !ok {
				copied := entry
				byPID[entry.pid] = &copied
				order = append(order, entry.pid)
				continue
			}
			if existing.cwd == "" {
				existing.cwd = entry.cwd
			}
			if existing.text == "" {
				existing.text = entry.text
			}
			if existing.name == "" {
				existing.name = entry.name
			}
			if existing.ppid == 0 {
				existing.ppid = entry.ppid
			}
		}
	}
	merged := make([]processEntry, 0, len(order))
	for _, pid := range order {
		merged = append(merged, *byPID[pid])
	}
	return merged
}

// Our own helpers are inside the folder for a moment while they inspect it; they are not users.
var inspectionHelpers = map[string]bool{"git": true, "lsof": true, "ps": true}

// ourHelpers collects the processes that only exist because this program is inspecting folders: its
// children that are Git, lsof or ps, its children caught between fork and exec (still carrying its own
// name), and everything those start in turn.
func ourHelpers(entries []processEntry, self int, selfName string) map[int]bool {
	helpers := map[int]bool{}
	for _, entry := range entries {
		if entry.ppid == self && (inspectionHelpers[entry.name] || (selfName != "" && entry.name == selfName)) {
			helpers[entry.pid] = true
		}
	}
	for changed := len(helpers) > 0; changed; {
		changed = false
		for _, entry := range entries {
			if !helpers[entry.pid] && helpers[entry.ppid] {
				helpers[entry.pid] = true
				changed = true
			}
		}
	}
	return helpers
}

type usageTarget struct {
	folder   string
	variants []string
}

// matchProcesses assigns each process to the deepest folder that holds it, so a worktree nested in
// another one (or in the main checkout) is not blamed for its parent's users.
func matchProcesses(entries []processEntry, folders []string, self int, selfName string) map[string][]ProcessUse {
	targets := make([]usageTarget, 0, len(folders))
	for _, folder := range folders {
		cleaned := filepath.Clean(folder)
		target := usageTarget{folder: folder, variants: []string{cleaned}}
		if resolved, err := filepath.EvalSymlinks(cleaned); err == nil && resolved != cleaned {
			target.variants = append(target.variants, resolved)
		}
		targets = append(targets, target)
	}
	sort.SliceStable(targets, func(i, j int) bool { return len(targets[i].variants[0]) > len(targets[j].variants[0]) })

	helpers := ourHelpers(entries, self, selfName)
	out := map[string][]ProcessUse{}
	for _, entry := range entries {
		if helpers[entry.pid] {
			continue
		}
		for _, target := range targets {
			if !entryUses(entry, target.variants) {
				continue
			}
			name := cleanProcessName(entry.name)
			out[target.folder] = append(out[target.folder], ProcessUse{PID: entry.pid, Command: name})
			break
		}
	}
	for folder := range out {
		sort.SliceStable(out[folder], func(i, j int) bool { return out[folder][i].PID < out[folder][j].PID })
	}
	return out
}

func entryUses(entry processEntry, variants []string) bool {
	for _, variant := range variants {
		if entry.cwd != "" && (entry.cwd == variant || strings.HasPrefix(entry.cwd, variant+string(filepath.Separator))) {
			return true
		}
		if entry.text != "" && (strings.Contains(entry.text, variant+string(filepath.Separator)) || strings.Contains(entry.text, variant+" ") || strings.HasSuffix(entry.text, variant)) {
			return true
		}
	}
	return false
}

func cleanProcessName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(name))
	if name == "" {
		name = "?"
	}
	if runes := []rune(name); len(runes) > 40 {
		name = string(runes[:40]) + "…"
	}
	return name
}

const describedProcesses = 3

// describeProcesses names the first few users for the reason shown to the person.
func describeProcesses(users []ProcessUse) string {
	parts := make([]string, 0, describedProcesses)
	for _, user := range users {
		if len(parts) == describedProcesses {
			break
		}
		parts = append(parts, user.Command+" (pid "+strconv.Itoa(user.PID)+")")
	}
	return strings.Join(parts, ", ")
}
