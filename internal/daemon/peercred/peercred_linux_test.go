//go:build linux

package peercred

import (
	"os"
	"slices"
	"strings"
	"testing"
)

const sampleStatus = `Name:	fling
Umask:	0022
State:	S (sleeping)
Pid:	4242
Uid:	1026	1026	1026	1026
Gid:	100	100	100	100
FDSize:	64
Groups:	100 101 65537 
NStgid:	4242
`

func TestParseProcStatusGroups(t *testing.T) {
	groups, err := parseProcStatusGroups(strings.NewReader(sampleStatus), 1026)
	if err != nil {
		t.Fatal(err)
	}
	if want := []uint32{100, 101, 65537}; !slices.Equal(groups, want) {
		t.Fatalf("got %v, want %v", groups, want)
	}
}

func TestParseProcStatusGroupsEmpty(t *testing.T) {
	status := strings.Replace(sampleStatus, "Groups:	100 101 65537 ", "Groups:	", 1)
	groups, err := parseProcStatusGroups(strings.NewReader(status), 1026)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Fatalf("got %v, want none", groups)
	}
}

func TestParseProcStatusGroupsUIDMismatch(t *testing.T) {
	// the pid was reused by another user's process
	if _, err := parseProcStatusGroups(strings.NewReader(sampleStatus), 1000); err == nil {
		t.Fatal("expected an error for a uid mismatch")
	}
}

func TestParseProcStatusGroupsMissingFields(t *testing.T) {
	if _, err := parseProcStatusGroups(strings.NewReader("Name:	fling\n"), 1026); err == nil {
		t.Fatal("expected an error when Uid and Groups are missing")
	}
}

func TestProcGroupsSelf(t *testing.T) {
	groups, err := procGroups(int32(os.Getpid()), uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.Getgroups()
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range want {
		if !slices.Contains(groups, uint32(g)) {
			t.Fatalf("group %d missing from %v", g, groups)
		}
	}
}
