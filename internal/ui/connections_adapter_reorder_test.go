package ui

import (
	"slices"
	"testing"

	"github.com/YoanWai/agent-manager/internal/remote"
	"github.com/YoanWai/agent-manager/internal/sessioncmd"
	"github.com/YoanWai/agent-manager/internal/store"
	uilink "github.com/YoanWai/agent-manager/internal/ui/connections/link"
	uirail "github.com/YoanWai/agent-manager/internal/ui/rail"
	tea "github.com/charmbracelet/bubbletea"
)

// connectionsModel stores and lists the named connections, none refreshed.
func connectionsModel(t *testing.T, fake *fakeSSH, names ...string) *Model {
	t.Helper()
	m := listedModel(t, fake)
	for _, name := range names {
		if err := m.services.store.AddConnection(store.Connection{Name: name, Destination: "me@" + name}); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := m.services.store.Connections()
	if err != nil {
		t.Fatal(err)
	}
	m.ssh = uilink.New(m.ssh.Client, stored)
	m.rebuildRows()
	return m
}

func listedConnections(m *Model) []string {
	var names []string
	for _, conn := range m.ssh.List() {
		names = append(names, conn.Name)
	}
	return names
}

func storedConnections(t *testing.T, m *Model) []string {
	t.Helper()
	stored, err := m.services.store.Connections()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, conn := range stored {
		names = append(names, conn.Name)
	}
	return names
}

// pressList sends a list key and runs all it started.
func (m *Model) pressList(t *testing.T, key string) {
	t.Helper()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	m.runRemoteCmd(t, cmd)
}

// A connection's place is this machine's own, so no host hears of a move,
// and a refresh that was under way still lands.
func TestReorderingAConnectionStaysOnThisMachine(t *testing.T) {
	fake := &fakeSSH{}
	m := connectionsModel(t, fake, "box", "gpu", "lab")
	refresh := m.ssh.PollHost("box")
	m.rail.Focus(uirail.Selection{Kind: uirail.ConnectionRow, Host: "box"})
	m.pressList(t, "J")
	want := []string{"gpu", "box", "lab"}
	if got := storedConnections(t, m); !slices.Equal(got, want) {
		t.Fatalf("stored = %v, want %v", got, want)
	}
	if got := listedConnections(m); !slices.Equal(got, want) {
		t.Fatalf("listed = %v, want %v", got, want)
	}
	if calls := fake.taken(); len(calls) != 0 {
		t.Fatalf("calls = %q, want none", calls)
	}
	m.applyTestMsg(t, refresh())
	if !hostState(m, "box").OK {
		t.Error("the refresh started before the move was dropped")
	}
	if selected, _ := m.rail.Selected(); selected != boxRow {
		t.Errorf("selected = %+v", selected)
	}
}

// Another manager on this machine removed the connection meanwhile.
func TestReorderingPastARemovedConnectionReportsIt(t *testing.T) {
	m := connectionsModel(t, &fakeSSH{}, "box", "gpu")
	if err := m.services.store.DeleteConnection("gpu"); err != nil {
		t.Fatal(err)
	}
	m.rail.Focus(boxRow)
	m.pressList(t, "J")
	if m.errBar.text != `connection "gpu": connection does not exist` {
		t.Errorf("status = %q", m.errBar.text)
	}
	if got := listedConnections(m); !slices.Equal(got, []string{"box", "gpu"}) {
		t.Errorf("listed = %v", got)
	}
}

// orderedModel lists box with a terminal between two agents, an archived
// agent among them and two groups, and the same ids and groups here.
func orderedModel(t *testing.T, fake *fakeSSH) *Model {
	t.Helper()
	m := listedModel(t, fake)
	for _, id := range []string{"s1", "s2"} {
		if err := m.services.store.CreateSession(store.Session{ID: id, Name: id, Tool: "claude", Cwd: "/tmp", Group: "web", Status: "idle"}); err != nil {
			t.Fatal(err)
		}
	}
	fake.snapshot.Groups = []sessioncmd.Group{{Path: "web"}, {Path: "ops"}}
	fake.snapshot.Order = []string{"s1", "t1", "s3", "s2"}
	setHost(m, "box", uilink.Facts(remote.HostState{OK: true, Snapshot: fake.snapshot}))
	return m
}

func boxListed(m *Model) []string {
	var ids []string
	for _, row := range hostState(m, "box").Rows() {
		ids = append(ids, row.ID)
	}
	return ids
}

func localOrder(t *testing.T, m *Model) []string {
	t.Helper()
	sessions, err := m.services.store.ListSessions(true)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, sess := range sessions {
		ids = append(ids, sess.ID)
	}
	return ids
}

// key sends a list key and returns what it started, unrun.
func (m *Model) key(key string) tea.Cmd {
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return cmd
}

// finishCall runs the call on its way to a host and returns what its answer
// started: the refresh that picks the host up again.
func (m *Model) finishCall(t *testing.T) tea.Cmd {
	t.Helper()
	if m.effects.Remote.Active == nil {
		t.Fatal("no call on its way")
	}
	_, cmd := m.Update(m.effects.Remote.Active.Command())
	if cmd == nil {
		t.Fatal("the answer started no refresh")
	}
	return cmd
}

func (m *Model) finishRemoteCall(t *testing.T) {
	t.Helper()
	m.applyTestMsg(t, m.finishCall(t)())
}

func TestReorderingARemoteRowTellsItsHostWhereItWent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target uirail.Selection
		key    string
		want   []string
		listed []string
	}{
		{"an agent", boxSession("s1"), "J", []string{"reorder-session", "--after=t1", "--json", "--", "s1"}, []string{"t1", "s1", "s3", "s2"}},
		{"a terminal past a hidden agent", boxSession("t1"), "J", []string{"reorder-session", "--after=s2", "--json", "--", "t1"}, []string{"s1", "s3", "s2", "t1"}},
		{"a group", boxGroup, "J", []string{"reorder-group", "--after=ops", "--json", "--", "web"}, []string{"s1", "t1", "s3", "s2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeSSH{}
			m := orderedModel(t, fake)
			before := localOrder(t, m)
			m.rail.Focus(tc.target)
			m.key(tc.key)
			// The list shows the move before the host has heard of it.
			if calls := fake.taken(); len(calls) != 0 {
				t.Fatalf("calls on the update path = %q", calls)
			}
			if got := boxListed(m); !slices.Equal(got, tc.listed) {
				t.Fatalf("listed = %v, want %v", got, tc.listed)
			}
			if selected, _ := m.rail.Selected(); selected != tc.target {
				t.Fatalf("selected = %+v", selected)
			}
			m.drainEffects(t)
			if calls := fake.taken(); len(calls) != 1 || !slices.Equal(calls[0], tc.want) {
				t.Fatalf("calls = %q, want one %q", calls, tc.want)
			}
			if m.errBar.text != "" {
				t.Errorf("status = %q", m.errBar.text)
			}
			if got := localOrder(t, m); !slices.Equal(got, before) {
				t.Errorf("this machine's rows = %v, want %v", got, before)
			}
		})
	}
}

// Each key moves the row here. The call on its way is let go, and one more
// carries where the last key left it.
func TestAHeldReorderKeySendsOnlyTheLastPlace(t *testing.T) {
	fake := &fakeSSH{}
	m := orderedModel(t, fake)
	m.rail.Focus(boxSession("s1"))
	m.key("J")
	active := m.effects.Remote.Active
	if active == nil {
		t.Fatal("the first move started no call")
	}
	for _, key := range []string{"J", "K", "J"} {
		m.key(key)
	}
	if got := boxListed(m); !slices.Equal(got, []string{"t1", "s3", "s2", "s1"}) {
		t.Fatalf("listed = %v", got)
	}
	if waiting := len(m.effects.Remote.Pending); waiting != 1 {
		t.Fatalf("%d calls wait, want one", waiting)
	}
	// A move of other rows waits its own turn.
	m.rail.Focus(boxGroup)
	m.key("J")
	if waiting := len(m.effects.Remote.Pending); waiting != 2 {
		t.Fatalf("%d calls wait, want two", waiting)
	}
	m.drainEffects(t)
	calls := fake.taken()
	calls = slices.DeleteFunc(calls, func(call []string) bool { return call[0] == "snapshot" })
	if len(calls) != 3 || !slices.Equal(calls[1], []string{"reorder-session", "--after=s2", "--json", "--", "s1"}) || calls[2][0] != "reorder-group" {
		t.Fatalf("calls = %q", calls)
	}
}

// A refresh that left before the move must not put the row back under the
// cursor, and the next one brings the host's own order.
func TestARefreshUnderWayDoesNotUndoARemoteMove(t *testing.T) {
	fake := &fakeSSH{}
	m := orderedModel(t, fake)
	refresh := m.ssh.PollHost("box")
	stale := refresh()
	m.rail.Focus(boxSession("s1"))
	m.key("J")
	m.applyTestMsg(t, stale)
	want := []string{"t1", "s1", "s3", "s2"}
	if got := boxListed(m); !slices.Equal(got, want) {
		t.Fatalf("listed after the stale refresh = %v, want %v", got, want)
	}
	fake.mu.Lock()
	fake.snapshot.Order = want
	fake.mu.Unlock()
	m.finishRemoteCall(t)
	if calls := fake.taken(); calls[len(calls)-1][0] != "snapshot" {
		t.Fatalf("calls = %q, want a refresh after the move", calls)
	}
	if got := boxListed(m); !slices.Equal(got, want) {
		t.Fatalf("listed after the move took = %v, want %v", got, want)
	}
}

func TestAHostThatRefusesAnOrderGetsItsRowsBack(t *testing.T) {
	fake := &fakeSSH{}
	m := orderedModel(t, fake)
	m.rail.Focus(boxSession("s1"))
	m.key("J")
	if got := boxListed(m); got[0] != "t1" {
		t.Fatalf("listed = %v, want the move shown", got)
	}
	fake.mu.Lock()
	fake.stderr, fake.code = "agent-manager: the list changed since it was read; try again\n", 1
	fake.mu.Unlock()
	refresh := m.finishCall(t)
	if m.errBar.text != "box: the list changed since it was read; try again" {
		t.Fatalf("status = %q", m.errBar.text)
	}
	fake.mu.Lock()
	fake.stderr, fake.code = "", 0
	fake.mu.Unlock()
	m.applyTestMsg(t, refresh())
	if got := boxListed(m); !slices.Equal(got, []string{"s1", "t1", "s3", "s2"}) {
		t.Fatalf("listed = %v, want the host's order", got)
	}
}

func TestAnOfflineHostRefusesAReorder(t *testing.T) {
	fake := &fakeSSH{}
	m := orderedModel(t, fake)
	down := hostState(m, "box")
	down.OK, down.Err, down.Failures = false, "box: unreachable over SSH", 1
	setHost(m, "box", down)
	for _, target := range []uirail.Selection{boxSession("s1"), boxGroup} {
		m.clearErr()
		m.rail.Focus(target)
		m.pressList(t, "J")
		if m.errBar.text != "box: unreachable over SSH" {
			t.Errorf("status = %q", m.errBar.text)
		}
	}
	if calls := fake.taken(); len(calls) != 0 {
		t.Errorf("calls = %q", calls)
	}
	if got := boxListed(m); !slices.Equal(got, []string{"s1", "t1", "s3", "s2"}) {
		t.Errorf("listed = %v", got)
	}
}

// liftRemote lifts the row by its handle, as a click on it does.
func (m *Model) liftRemote(t *testing.T, target uirail.Selection) {
	t.Helper()
	m.rail.Focus(target)
	preparedView(m)
	top, _ := m.bodyYRange()
	x, y := -1, -1
	for line, painted := range m.layout.displayedRail.Lines {
		if painted.HitOK && painted.Hit == target {
			x, y = m.layout.displayedRail.Handles["rs:box::"+target.SessionID], top+line
			break
		}
	}
	if x <= 0 {
		t.Fatalf("no handle on %+v", target)
	}
	for _, action := range []tea.MouseAction{tea.MouseActionPress, tea.MouseActionRelease} {
		m.handleMouse(tea.MouseMsg{X: x, Y: y, Action: action, Button: tea.MouseButtonLeft})
	}
	if !m.rail.Reordering() {
		t.Fatal("the row was not lifted")
	}
}

// After a refusal the host's rows come back, so there is nothing for esc to
// put back: undoing the refused steps would move the row the other way.
func TestARefusedMoveEndsTheLift(t *testing.T) {
	fake := &fakeSSH{}
	m := orderedModel(t, fake)
	m.liftRemote(t, boxSession("s1"))
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	fake.mu.Lock()
	fake.stderr, fake.code = "agent-manager: the list changed since it was read; try again\n", 1
	fake.mu.Unlock()
	refresh := m.finishCall(t)
	if m.rail.Reordering() {
		t.Error("the row is still lifted after the refusal")
	}
	fake.mu.Lock()
	fake.stderr, fake.code = "", 0
	fake.mu.Unlock()
	m.applyTestMsg(t, refresh())
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m.drainEffects(t)
	for _, call := range fake.taken()[1:] {
		if call[0] == "reorder-session" {
			t.Errorf("esc sent %q", call)
		}
	}
}

func TestAHostGoingOfflineEndsTheLift(t *testing.T) {
	m := orderedModel(t, &fakeSSH{})
	m.liftRemote(t, boxSession("s1"))
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.drainEffects(t)
	down := hostState(m, "box")
	down.OK, down.Err, down.Failures = false, "box: unreachable over SSH", 1
	setHost(m, "box", down)
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.rail.Reordering() || m.errBar.text != "box: unreachable over SSH" {
		t.Errorf("lifted %v, status %q", m.rail.Reordering(), m.errBar.text)
	}
}

// A quit drains the calls it accepted, so a move made while it drains must
// not take one away.
func TestAQuitKeepsTheOrderAlreadyAccepted(t *testing.T) {
	fake := &fakeSSH{}
	m := orderedModel(t, fake)
	m.rail.Focus(boxSession("s1"))
	m.key("J")
	m.key("J")
	m.effects.Quitting = true
	m.queueReorder(uilink.Call{Op: uilink.ReorderSession, Host: "box", Group: "web", Ref: remote.Ref{Host: "box", ID: "s1"}, Anchor: "late"})
	if waiting := len(m.effects.Remote.Pending); waiting != 1 {
		t.Fatalf("%d calls wait, want the accepted one", waiting)
	}
}

// A call that changes which rows are siblings waits between two moves of
// them. The first was made before it and must reach the host before it.
func TestAMoveQueuedAfterAnotherCallKeepsTheOneBeforeIt(t *testing.T) {
	fake := &fakeSSH{}
	m := orderedModel(t, fake)
	m.rail.Focus(boxGroup)
	m.key("J")
	m.rail.Focus(boxSession("s1"))
	m.key("J")
	m.queueRemote(uilink.Call{Op: uilink.Move, Host: "box", Ref: remote.Ref{Host: "box", ID: "s2"}, Name: "s2", Group: "ops"})
	m.key("J")
	m.drainEffects(t)
	var commands []string
	for _, call := range fake.taken() {
		if call[0] != "snapshot" {
			commands = append(commands, call[0])
		}
	}
	if !slices.Equal(commands, []string{"reorder-group", "reorder-session", "move", "reorder-session"}) {
		t.Fatalf("calls = %q", commands)
	}
}

// The rows a refused call arranged are not the lifted row's.
func TestARefusedMoveOfOtherRowsKeepsTheLift(t *testing.T) {
	fake := &fakeSSH{}
	m := orderedModel(t, fake)
	m.rail.Focus(boxGroup)
	m.key("J")
	m.liftRemote(t, boxSession("s1"))
	fake.mu.Lock()
	fake.stderr, fake.code = "agent-manager: the list changed since it was read; try again\n", 1
	fake.mu.Unlock()
	m.finishCall(t)
	if !m.rail.Reordering() {
		t.Error("a refused move of a group dropped the lifted agent")
	}
}

// A waiting move of one row can be where another was put, so it has to reach
// the host before that one even when its row moves again.
func TestAMoveAnotherLeansOnIsNotReplaced(t *testing.T) {
	fake := &fakeSSH{}
	m := orderedModel(t, fake)
	m.rail.Focus(boxGroup)
	m.key("J")
	for _, step := range []struct{ id, key string }{{"s1", "J"}, {"s1", "J"}, {"s2", "J"}, {"s1", "K"}} {
		m.rail.Focus(boxSession(step.id))
		m.key(step.key)
	}
	m.drainEffects(t)
	var moves [][]string
	for _, call := range fake.taken() {
		if call[0] == "reorder-session" {
			moves = append(moves, []string{call[1], call[4]})
		}
	}
	want := [][]string{{"--after=s2", "s1"}, {"--after=s1", "s2"}, {"--before=t1", "s1"}}
	if !slices.EqualFunc(moves, want, slices.Equal) {
		t.Fatalf("moves = %q, want %q", moves, want)
	}
}

// The host is told the place the row was lifted from, beside the hidden s3,
// rather than the steps back.
func TestEscTellsTheHostWhereTheRowWas(t *testing.T) {
	fake := &fakeSSH{}
	m := orderedModel(t, fake)
	m.liftRemote(t, boxSession("s2"))
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := boxListed(m); !slices.Equal(got, []string{"s1", "t1", "s3", "s2"}) || m.rail.Reordering() {
		t.Fatalf("listed = %v, lifted %v", got, m.rail.Reordering())
	}
	m.drainEffects(t)
	var moves [][]string
	for _, call := range fake.taken() {
		if call[0] == "reorder-session" {
			moves = append(moves, []string{call[1], call[4]})
		}
	}
	if want := [][]string{{"--before=t1", "s2"}, {"--after=s3", "s2"}}; !slices.EqualFunc(moves, want, slices.Equal) {
		t.Fatalf("moves = %q, want %q", moves, want)
	}
}
