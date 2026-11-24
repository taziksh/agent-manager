package rail

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestResolveDropKindsStayInsideRailPolicy(t *testing.T) {
	model := New(nil)
	model.Reconcile(Snapshot{Groups: []string{"alpha", "beta"}, Sessions: []Session{
		{ID: "mover", Name: "mover", Group: "alpha"},
		{ID: "agent", Name: "agent", Group: "beta"},
		{ID: "shell", Name: "shell", Group: "alpha", IsShell: true},
	}})

	model.Focus(Selection{Kind: SessionRow, SessionID: "mover", Group: "alpha"})
	model.enterReorder("s:mover")
	if drop, ok := model.resolveDrop(Selection{Kind: GroupRow, Group: "beta"}); !ok || drop.kind != dropInto {
		t.Fatalf("session onto group = %+v, %v; want dropInto", drop, ok)
	}

	model.finishReorder()
	model.Focus(Selection{Kind: SessionRow, SessionID: "shell", Group: "alpha"})
	model.enterReorder("s:shell")
	if drop, ok := model.resolveDrop(Selection{Kind: SessionRow, SessionID: "agent", Group: "beta"}); !ok || drop.kind != dropUnder {
		t.Fatalf("shell onto agent = %+v, %v; want dropUnder", drop, ok)
	}
}

func TestFilteredRailRefusesCrossLevelDrop(t *testing.T) {
	model := New(nil)
	model.Reconcile(Snapshot{Groups: []string{"alpha", "beta"}, Sessions: []Session{
		{ID: "mover", Name: "mover", Group: "alpha"}, {ID: "stay", Name: "stay", Group: "beta"},
	}})
	model.SetSearch("e", false)
	model.Focus(Selection{Kind: SessionRow, SessionID: "mover", Group: "alpha"})
	model.enterReorder("s:mover")
	if drop, ok := model.resolveDrop(Selection{Kind: GroupRow, Group: "beta"}); ok {
		t.Fatalf("filtered cross-level drop = %+v", drop)
	}
}

func TestStaleAutoscrollTickCannotSteerANewerLift(t *testing.T) {
	model := New(nil)
	model.Reconcile(Snapshot{Sessions: []Session{{ID: "one", Name: "one"}, {ID: "two", Name: "two"}}})
	model.Focus(Selection{Kind: SessionRow, SessionID: "one"})
	model.enterReorder("s:one")
	stale := model.reorder.lift
	model.finishReorder()
	model.enterReorder("s:one")
	model.reorder.autoscroll = autoscrollState{edge: 1, running: true}

	decision := model.ApplyAutoScroll(AutoScrollTick{Lift: stale}, Frame{}, MouseContext{})
	if !decision.Consumed || !model.reorder.autoscroll.running || model.reorder.autoscroll.anchored {
		t.Fatalf("stale tick changed scroll = %+v decision = %+v", model.reorder.autoscroll, decision)
	}
	// The newer lift already runs its own tick; a second would scroll it
	// twice as fast.
	if decision.AutoScroll != nil {
		t.Fatalf("stale tick scheduled another tick: %+v", decision.AutoScroll)
	}
}

func TestDragEdgeSchedulesTypedAutoscroll(t *testing.T) {
	model := New(nil)
	for index := 0; index < 20; index++ {
		model.snapshot.Sessions = append(model.snapshot.Sessions, Session{ID: string(rune('a' + index)), Name: "row", CreatedAt: time.Now()})
	}
	model.rebuildRows()
	model.Focus(model.rows[1].selection())
	model.enterReorder(model.rows[1].key())
	frame := model.Render(RenderContext{Width: 30, Height: 5, TerminalWidth: 80, TerminalHeight: 24}, Frame{})
	last := len(frame.Lines) - 1
	decision := model.Mouse(tea.MouseMsg{X: 2, Y: last, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft}, frame, MouseContext{FullWidth: true})
	if decision.AutoScroll == nil || decision.AutoScroll.Lift != model.reorder.lift {
		t.Fatalf("autoscroll request = %+v", decision.AutoScroll)
	}
}

// A lifted row can go under the drag: its agent is deleted, or its host's
// connection removed. The cursor then rests on another row, which must stay.
func TestALiftedRowThatIsGoneMovesNoOther(t *testing.T) {
	rows := func(ids ...string) Snapshot {
		host := Host{Name: "box", State: HostOnline}
		for _, id := range ids {
			host.Sessions = append(host.Sessions, Session{ID: id, Name: id, Tool: "claude"})
		}
		return Snapshot{Hosts: []Host{host}}
	}
	model := New(nil)
	model.Reconcile(rows("a", "b", "c", "d", "e"))
	model.Focus(Selection{Kind: SessionRow, SessionID: "c", Host: "box"})
	model.enterReorder("rs:box::c")
	applied(&model, model.stepLifted(-1))
	model.Reconcile(rows("b", "a", "d", "e"))
	if mutations := model.planDrag(Selection{Kind: SessionRow, SessionID: "b", Host: "box"}); len(mutations) != 0 {
		t.Errorf("the drag moved %+v", mutations)
	}
	if decision := model.reorderKey(tea.KeyMsg{Type: tea.KeyEsc}, KeyContext{}); len(decision.Mutations) != 0 || model.Reordering() {
		t.Errorf("esc moved %+v, still lifted %v", decision.Mutations, model.Reordering())
	}
}

func TestLiftedAmongNamesTheRowsACallArranged(t *testing.T) {
	agents := Siblings{Host: "box", Group: "ops"}
	nested := Siblings{Host: "box", Group: "ops", Parent: "deep"}
	groups := Siblings{Host: "box", Groups: true}
	for _, tc := range []struct {
		lifted Selection
		want   Siblings
	}{
		{Selection{Kind: SessionRow, SessionID: "deep", Group: "ops", Host: "box"}, agents},
		{Selection{Kind: SessionRow, SessionID: "shell", Group: "ops", Host: "box"}, nested},
		{Selection{Kind: GroupRow, Group: "ops", Host: "box"}, groups},
		{Selection{Kind: ConnectionRow, Host: "box"}, Siblings{}},
		{Selection{Kind: SessionRow, SessionID: "near", Group: "work"}, Siblings{}},
	} {
		model := New(nil)
		model.Reconcile(hostsSnapshot(HostOnline))
		if model.LiftedAmong(tc.want) {
			t.Errorf("%+v: among %+v before it was lifted", tc.lifted, tc.want)
		}
		model.Focus(tc.lifted)
		model.enterReorder(tc.lifted.key())
		for _, siblings := range []Siblings{agents, nested, groups} {
			other := siblings
			other.Host = "gpu"
			if got := model.LiftedAmong(siblings); got != (siblings == tc.want) || model.LiftedAmong(other) {
				t.Errorf("%+v among %+v = %v", tc.lifted, siblings, got)
			}
		}
		model.EndLift()
		if model.Reordering() {
			t.Errorf("%+v is still lifted", tc.lifted)
		}
	}
}
