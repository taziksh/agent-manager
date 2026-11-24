package sessioncmd

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/YoanWai/agent-manager/internal/store"
)

func TestReorderSessionWithNoCallerShowsInTheSnapshot(t *testing.T) {
	h := newStoreSessionHarness(t)
	for _, id := range []string{"aaaa1111", "bbbb2222", "cccc3333"} {
		tool := "echoer"
		if id == "bbbb2222" {
			tool = "terminal"
		}
		if err := h.store.CreateSession(store.Session{ID: id, Name: id, Tool: tool, Cwd: h.caller.Cwd, Group: "fleet", Status: "idle"}); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"cccc3333", "bbbb2222", "aaaa1111"}
	for _, move := range []struct {
		id, anchor string
		after      bool
	}{{"cccc3333", "aaaa1111", false}, {"aaaa1111", "bbbb2222", true}} {
		if moved, err := h.sessions.ReorderSession("", move.id, move.anchor, move.after); err != nil || moved.ID != move.id || moved.Group != "fleet" {
			t.Fatalf("ReorderSession(%+v) = %+v, %v", move, moved, err)
		}
	}
	snapshot, err := h.sessions.Snapshot("")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	// The terminal keeps its place between the agents, which the separate
	// lists alone cannot say.
	var got []string
	for _, id := range snapshot.Order {
		if slices.Contains(want, id) {
			got = append(got, id)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("snapshot order = %v, want %v", got, want)
	}
	if len(snapshot.Order) != len(snapshot.Sessions)+len(snapshot.Terminals) {
		t.Fatalf("snapshot order names %d of %d rows", len(snapshot.Order), len(snapshot.Sessions)+len(snapshot.Terminals))
	}
}

func TestReorderGroupWithNoCallerShowsInTheSnapshot(t *testing.T) {
	h := newStoreSessionHarness(t)
	for _, path := range []string{"fleet", "fleet/a", "fleet/b"} {
		if _, err := h.sessions.CreateGroup("", path, ""); err != nil {
			t.Fatalf("CreateGroup %s: %v", path, err)
		}
	}
	if err := h.store.CreateSession(store.Session{ID: "aaaa1111", Name: "a", Tool: "echoer", Cwd: h.caller.Cwd, Group: "fleet/b", Status: "idle"}); err != nil {
		t.Fatal(err)
	}
	if moved, err := h.sessions.ReorderGroup("", " fleet/b/ ", "/fleet/a", false); err != nil || moved.Path != "fleet/b" || moved.Sessions != 1 {
		t.Fatalf("ReorderGroup = %+v, %v", moved, err)
	}
	snapshot, err := h.sessions.Snapshot("")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	var got []string
	for _, group := range snapshot.Groups {
		if strings.HasPrefix(group.Path, "fleet/") {
			got = append(got, group.Path)
		}
	}
	if !slices.Equal(got, []string{"fleet/b", "fleet/a"}) {
		t.Fatalf("groups = %v", got)
	}
}

func TestReorderRefusesACallerAndAStaleList(t *testing.T) {
	h := newStoreSessionHarness(t)
	if _, err := h.sessions.ReorderSession(h.caller.ID, h.caller.ID, "dead0000", true); err == nil || !strings.Contains(err.Error(), "reordering is for the user's reorder keys") {
		t.Errorf("session with a caller: %v", err)
	}
	if _, err := h.sessions.ReorderGroup(h.caller.ID, h.caller.Group, "nosuch", true); err == nil || !strings.Contains(err.Error(), "reordering is for the user's reorder keys") {
		t.Errorf("group with a caller: %v", err)
	}
	if _, err := h.sessions.ReorderSession("", h.caller.ID, "dead0000", true); !errors.Is(err, store.ErrOrderChanged) {
		t.Errorf("session beside one that is gone: %v", err)
	}
	if _, err := h.sessions.ReorderGroup("", h.caller.Group, "nosuch", true); !errors.Is(err, store.ErrOrderChanged) {
		t.Errorf("group beside one that is gone: %v", err)
	}
}
