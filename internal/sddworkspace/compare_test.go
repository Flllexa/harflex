package sddworkspace

import (
	"reflect"
	"testing"
)

func TestCompareReportsSortedAddDeleteContentModeTypeAndTargetChanges(t *testing.T) {
	before := Manifest{Entries: []Entry{
		{Path: "content", Type: EntryFile, Mode: 0o100644, Size: 3, SHA256: "old"},
		{Path: "deleted", Type: EntryFile, Mode: 0o100644, Size: 0, SHA256: emptyContentSHA256},
		{Path: "exec", Type: EntryFile, Mode: 0o100644, Size: 1, SHA256: "same"},
		{Path: "link", Type: EntrySymlink, Mode: 0o120000, Size: 3, SHA256: "old", Target: "old"},
		{Path: "replace", Type: EntryFile, Mode: 0o100644, Size: 1, SHA256: "same"},
	}}
	after := Manifest{Entries: []Entry{
		{Path: "content", Type: EntryFile, Mode: 0o100644, Size: 3, SHA256: "new"},
		{Path: "exec", Type: EntryFile, Mode: 0o100755, Size: 1, SHA256: "same"},
		{Path: "link", Type: EntrySymlink, Mode: 0o120000, Size: 3, SHA256: "new", Target: "new"},
		{Path: "new", Type: EntryFile, Mode: 0o100644, Size: 0, SHA256: emptyContentSHA256},
		{Path: "replace", Type: EntryDirectory, Mode: 0o040000, Size: 0, SHA256: emptyContentSHA256},
	}}

	got := Compare(before, after)
	want := []Change{
		{Path: "content", Kind: ChangeContent},
		{Path: "deleted", Kind: ChangeDeleted},
		{Path: "exec", Kind: ChangeMode},
		{Path: "link", Kind: ChangeTarget},
		{Path: "new", Kind: ChangeAdded},
		{Path: "replace", Kind: ChangeType},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Compare() = %#v, want %#v", got, want)
	}
}
