package externalagent

import "strings"

// NewOpenCode creates the adapter; an empty path uses known desktop locations, then PATH.
// The CLI accepts its prompt through argv, where local process inspection may see it.
func NewOpenCode(path string) Adapter {
	return &cliAdapter{id: "opencode", path: path, resumable: true, newNormalizer: newOpenCodeNormalizer, build: func(r Request) ([]string, string) {
		args := []string{"--pure", "run", "--format", "json", "--dir", r.CWD}
		if r.Model != "" {
			args = append(args, "--model", r.Model)
		}
		if r.SessionID != "" {
			args = append(args, "--session", r.SessionID)
		}
		if strings.HasPrefix(r.Prompt, "-") {
			args = append(args, "--")
		}
		return append(args, r.Prompt), ""
	}}
}
