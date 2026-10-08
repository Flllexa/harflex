package sddworkspace

import "sort"

// ChangeKind is one manifest-level difference. File content, Git-relevant mode,
// entry type, and symlink text are separate so mode-only and target-only changes
// remain visible without dereferencing links.
type ChangeKind string

const (
	ChangeAdded   ChangeKind = "added"
	ChangeDeleted ChangeKind = "deleted"
	ChangeContent ChangeKind = "content"
	ChangeMode    ChangeKind = "mode"
	ChangeType    ChangeKind = "type"
	ChangeTarget  ChangeKind = "target"
)

// Change describes one sorted difference between two manifests.
type Change struct {
	Path string     `json:"path"`
	Kind ChangeKind `json:"kind"`
}

// Compare reports sorted changes between manifest snapshots. It operates only
// on the recorded metadata and never opens or follows filesystem paths.
func Compare(before, after Manifest) []Change {
	left := make(map[string]Entry, len(before.Entries))
	right := make(map[string]Entry, len(after.Entries))
	paths := make(map[string]struct{}, len(before.Entries)+len(after.Entries))
	for _, entry := range before.Entries {
		left[entry.Path] = entry
		paths[entry.Path] = struct{}{}
	}
	for _, entry := range after.Entries {
		right[entry.Path] = entry
		paths[entry.Path] = struct{}{}
	}
	orderedPaths := make([]string, 0, len(paths))
	for path := range paths {
		orderedPaths = append(orderedPaths, path)
	}
	sort.Strings(orderedPaths)

	changes := make([]Change, 0)
	for _, path := range orderedPaths {
		oldEntry, wasPresent := left[path]
		newEntry, isPresent := right[path]
		switch {
		case !wasPresent:
			changes = append(changes, Change{Path: path, Kind: ChangeAdded})
		case !isPresent:
			changes = append(changes, Change{Path: path, Kind: ChangeDeleted})
		case oldEntry.Type != newEntry.Type:
			changes = append(changes, Change{Path: path, Kind: ChangeType})
		case oldEntry.Type == EntryFile:
			if oldEntry.Size != newEntry.Size || oldEntry.SHA256 != newEntry.SHA256 {
				changes = append(changes, Change{Path: path, Kind: ChangeContent})
			}
			if oldEntry.Mode != newEntry.Mode {
				changes = append(changes, Change{Path: path, Kind: ChangeMode})
			}
		case oldEntry.Type == EntrySymlink && oldEntry.Target != newEntry.Target:
			changes = append(changes, Change{Path: path, Kind: ChangeTarget})
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Path != changes[j].Path {
			return changes[i].Path < changes[j].Path
		}
		return changes[i].Kind < changes[j].Kind
	})
	return changes
}
