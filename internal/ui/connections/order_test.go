package connections

import (
	"slices"
	"testing"
	"time"

	remote "github.com/YoanWai/agent-manager/internal/remote/value"
)

// webHost lists a terminal between two agents, as only the host's order can
// say, and an archived agent after them.
func webHost(order ...string) Host {
	return Host{
		OK:     true,
		Groups: []Group{{Path: "web"}, {Path: "ops"}, {Path: "lab"}},
		Sessions: []Session{
			{ID: "s1", Group: "web"}, {ID: "s2", Group: "web"}, {ID: "s3", Group: "web", Archived: true}, {ID: "o1", Group: "ops"},
		},
		Terminals: []Session{{ID: "t1", Group: "web"}, {ID: "n1", Group: "web", ParentID: "s1"}, {ID: "n2", Group: "web", ParentID: "s1"}},
		Order:     order,
	}
}

func connected(order ...string) *Connections {
	var c Connections
	c.SetList([]remote.Connection{box})
	c.Refreshed("box", c.Gen(), webHost(order...))
	return &c
}

func listed(c *Connections) []string {
	host, _ := c.Host("box")
	var ids []string
	for _, row := range host.Rows() {
		ids = append(ids, row.ID)
	}
	return ids
}

func listedGroups(c *Connections) []string {
	host, _ := c.Host("box")
	var paths []string
	for _, group := range host.Groups {
		paths = append(paths, group.Path)
	}
	return paths
}

func TestRowsFollowTheHostsOrder(t *testing.T) {
	if got := listed(connected("s1", "n1", "n2", "t1", "s2", "s3", "o1")); !slices.Equal(got, []string{"s1", "n1", "n2", "t1", "s2", "s3", "o1"}) {
		t.Errorf("rows = %v", got)
	}
	// A host that predates the order lists its terminals after its agents.
	if got := listed(connected()); !slices.Equal(got, []string{"s1", "s2", "s3", "o1", "t1", "n1", "n2"}) {
		t.Errorf("rows without an order = %v", got)
	}
	// An id the order misses goes last, and one it repeats or invents adds
	// no row.
	if got := listed(connected("t1", "gone", "s2", "t1", "s1", "n1", "n2", "o1")); !slices.Equal(got, []string{"t1", "s2", "s1", "n1", "n2", "o1", "s3"}) {
		t.Errorf("rows with a partial order = %v", got)
	}
	host, _ := connected("t1", "s1").Host("box")
	for _, row := range host.Rows() {
		if terminal := row.ID[0] == 't' || row.ID[0] == 'n'; row.Terminal != terminal {
			t.Errorf("row %s: terminal = %v", row.ID, row.Terminal)
		}
	}
}

func TestPlaceSessionMovesOneRowAmongItsSiblings(t *testing.T) {
	rows := []string{"s1", "n1", "n2", "t1", "s3", "s2", "o1"}
	for _, tc := range []struct {
		id, anchor    string
		after         bool
		group, parent string
		want          []string
	}{
		{"t1", "s2", true, "web", "", []string{"s1", "n1", "n2", "s3", "s2", "t1", "o1"}},
		{"s2", "t1", false, "web", "", []string{"s1", "n1", "n2", "s2", "t1", "s3", "o1"}},
		{"s1", "t1", true, "web", "", []string{"n1", "n2", "t1", "s1", "s3", "s2", "o1"}},
		{"n2", "n1", false, "web", "s1", []string{"s1", "n2", "n1", "t1", "s3", "s2", "o1"}},
	} {
		c := connected(rows...)
		group, parent, ok := c.PlaceSession("box", tc.id, tc.anchor, tc.after)
		if got := listed(c); !ok || group != tc.group || parent != tc.parent || !slices.Equal(got, tc.want) {
			t.Errorf("%+v: %q %q %v, rows = %v", tc, group, parent, ok, got)
		}
	}
	c := connected(rows...)
	for _, pair := range [][2]string{{"s1", "o1"}, {"s1", "n1"}, {"s1", "gone"}, {"gone", "s1"}} {
		if _, _, ok := c.PlaceSession("box", pair[0], pair[1], true); ok {
			t.Errorf("placed %v", pair)
		}
	}
	if _, _, ok := c.PlaceSession("gone", "s1", "s2", true); ok {
		t.Error("placed on a host with no rows")
	}
	if got := listed(c); !slices.Equal(got, rows) {
		t.Errorf("rows = %v", got)
	}
}

func TestPlaceGroupMovesOneGroupAmongItsSiblings(t *testing.T) {
	siblings := []string{"web", "ops", "lab", "implied"}
	for _, tc := range []struct {
		path, anchor string
		after        bool
		want         []string
	}{
		{"lab", "web", false, []string{"lab", "web", "ops", "implied"}},
		{"web", "implied", true, []string{"ops", "lab", "implied", "web"}},
		{"implied", "ops", false, []string{"web", "implied", "ops", "lab"}},
	} {
		c := connected()
		if ok := c.PlaceGroup("box", tc.path, tc.anchor, tc.after, siblings); !ok || !slices.Equal(listedGroups(c), tc.want) {
			t.Errorf("%+v: %v, groups = %v", tc, ok, listedGroups(c))
		}
	}
	if connected().PlaceGroup("box", "lab", "gone", true, siblings[:3]) {
		t.Error("placed beside a group that is gone")
	}
}

// A refresh that could have read the host before the move took must not put
// the rows back, and one started after it must.
func TestARefreshFromBeforeAMoveKeepsTheMove(t *testing.T) {
	c := connected("s1", "n1", "n2", "t1", "s2", "s3", "o1")
	now := time.Now()
	land := func() {
		t.Helper()
		fresh := webHost("s1", "n1", "n2", "t1", "s2", "s3", "new", "o1")
		fresh.Sessions = append(fresh.Sessions, Session{ID: "new", Group: "web"})
		if !c.Refreshed("box", c.Gen(), fresh) {
			t.Fatal("the refresh was dropped")
		}
	}
	moved := []string{"t1", "s1", "n1", "n2", "s2", "s3", "new", "o1"}

	c.StartRefresh("box", now)
	c.PlaceSession("box", "t1", "s1", false)
	c.PlaceGroup("box", "lab", "web", false, []string{"web", "ops", "lab"})
	c.HoldOrder("box")
	land()
	if got := listed(c); !slices.Equal(got, moved) {
		t.Fatalf("a refresh from before the call = %v, want %v", got, moved)
	}
	if got := listedGroups(c); !slices.Equal(got, []string{"lab", "web", "ops"}) {
		t.Fatalf("groups = %v", got)
	}

	c.StartRefresh("box", now)
	c.ReleaseOrder("box", true)
	land()
	if got := listed(c); !slices.Equal(got, moved) {
		t.Fatalf("a refresh from during the call = %v, want %v", got, moved)
	}

	c.StartRefresh("box", now)
	land()
	if got := listed(c); !slices.Equal(got, []string{"s1", "n1", "n2", "t1", "s2", "s3", "new", "o1"}) {
		t.Fatalf("a refresh from after the call = %v, want the host's order", got)
	}
	if got := listedGroups(c); !slices.Equal(got, []string{"web", "ops", "lab"}) {
		t.Fatalf("groups = %v", got)
	}
}

// A second move queued while the first is on its way holds the order until
// both are done.
func TestEveryPendingMoveHoldsTheOrder(t *testing.T) {
	c := connected("s1", "s2", "t1", "s3", "o1", "n1", "n2")
	c.PlaceSession("box", "s1", "s2", true)
	c.HoldOrder("box")
	c.HoldOrder("box")
	c.ReleaseOrder("box", true)
	c.StartRefresh("box", time.Now())
	c.Refreshed("box", c.Gen(), webHost("s1", "s2", "t1", "s3", "o1", "n1", "n2"))
	if got := listed(c); got[0] != "s2" {
		t.Fatalf("rows = %v, want the move kept", got)
	}
}

// A host that stops answering reports the rows of its last good refresh,
// which is older than the move.
func TestAFailedRefreshBringsNoOrder(t *testing.T) {
	c := connected("s1", "s2", "t1", "s3", "o1", "n1", "n2")
	c.PlaceSession("box", "s1", "s2", true)
	c.HoldOrder("box")
	c.ReleaseOrder("box", true)
	c.StartRefresh("box", time.Now())
	failed := webHost("s1", "s2", "t1", "s3", "o1", "n1", "n2")
	failed.OK, failed.Err = false, "box: unreachable over SSH"
	c.Refreshed("box", c.Gen(), failed)
	if got := listed(c); got[0] != "s2" {
		t.Fatalf("rows = %v, want the move kept", got)
	}
}

// Two paths a host sends can clean to the same text.
func TestKeepOrderLeavesRepeatsInPlace(t *testing.T) {
	key := func(s string) string { return s[:1] }
	for _, tc := range []struct{ fresh, shown, want []string }{
		{[]string{"a1", "b1", "a2", "c1"}, []string{"c", "a"}, []string{"c1", "b1", "a2", "a1"}},
		{[]string{"a1", "b1"}, []string{"b", "a", "b"}, []string{"b1", "a1"}},
		{nil, []string{"a"}, nil},
		{[]string{"a1"}, nil, []string{"a1"}},
	} {
		if got := keepOrder(tc.fresh, tc.shown, key); !slices.Equal(got, tc.want) {
			t.Errorf("keepOrder(%v, %v) = %v, want %v", tc.fresh, tc.shown, got, tc.want)
		}
	}
}

// Groups only session paths name are in the list but not in a snapshot's
// groups, so a refresh that keeps the order has to keep them too.
func TestARefreshDuringAMoveKeepsGroupsOnlySessionsName(t *testing.T) {
	var c Connections
	c.SetList([]remote.Connection{box})
	host := Host{OK: true, Sessions: []Session{{ID: "1", Group: "a"}, {ID: "2", Group: "b/deep"}, {ID: "3", Group: "c"}}}
	c.Refreshed("box", c.Gen(), host)
	c.StartRefresh("box", time.Now())
	c.PlaceGroup("box", "a", "b", true, []string{"a", "b", "c"})
	c.HoldOrder("box")
	gone := host
	gone.Sessions = gone.Sessions[:2]
	c.Refreshed("box", c.Gen(), gone)
	if got := listedGroups(&c); !slices.Equal(got, []string{"b", "a"}) {
		t.Fatalf("groups = %v, want the move kept without the group that is gone", got)
	}
}

// The host never took the move, so the rows it last gave come back even
// while it does not answer.
func TestAMoveTheHostNeverTookDoesNotOutliveAnOutage(t *testing.T) {
	c := connected("s1", "s2", "t1", "s3", "o1", "n1", "n2")
	c.PlaceSession("box", "s1", "s2", true)
	c.HoldOrder("box")
	c.ReleaseOrder("box", false)
	c.StartRefresh("box", time.Now())
	failed := webHost("s1", "s2", "t1", "s3", "o1", "n1", "n2")
	failed.OK, failed.Err = false, "box: unreachable over SSH"
	c.Refreshed("box", c.Gen(), failed)
	if got := listed(c); got[0] != "s1" {
		t.Fatalf("rows = %v, want the order the host last gave", got)
	}
}

// Once the host's own order is back, a later move it takes outlives an outage
// again.
func TestARefusalIsOverOnceTheHostsOrderIsBack(t *testing.T) {
	order := []string{"s1", "s2", "t1", "s3", "o1", "n1", "n2"}
	c := connected(order...)
	land := func(ok bool) {
		c.StartRefresh("box", time.Now())
		host := webHost(order...)
		host.OK = ok
		c.Refreshed("box", c.Gen(), host)
	}
	c.PlaceSession("box", "s1", "s2", true)
	c.HoldOrder("box")
	c.ReleaseOrder("box", false)
	land(true)
	c.PlaceSession("box", "s1", "s2", true)
	c.HoldOrder("box")
	c.ReleaseOrder("box", true)
	land(false)
	if got := listed(c); got[0] != "s2" {
		t.Fatalf("rows = %v, want the move the host took", got)
	}
}

// A stored subgroup names its parent the way a session's path does.
func TestARefreshDuringAMoveKeepsAGroupOnlyASubgroupNames(t *testing.T) {
	var c Connections
	c.SetList([]remote.Connection{box})
	host := Host{OK: true, Groups: []Group{{Path: "apps"}, {Path: "infra/db"}}}
	c.Refreshed("box", c.Gen(), host)
	c.StartRefresh("box", time.Now())
	c.PlaceGroup("box", "infra", "apps", false, []string{"apps", "infra"})
	c.HoldOrder("box")
	c.Refreshed("box", c.Gen(), host)
	if got := listedGroups(&c); !slices.Equal(got, []string{"infra", "apps", "infra/db"}) {
		t.Fatalf("groups = %v", got)
	}
}
