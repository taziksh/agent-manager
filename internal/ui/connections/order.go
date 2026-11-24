package connections

import (
	"slices"

	uirail "github.com/YoanWai/agent-manager/internal/ui/rail"
)

// Listed is a row as its host lists it.
type Listed struct {
	Session
	Terminal bool
}

// Rows is the host's agents and terminals in its own order. A row the order
// does not name goes last, agents before terminals, which is every row of a
// host that predates the order.
func (h Host) Rows() []Listed {
	rows := make([]Listed, 0, len(h.Sessions)+len(h.Terminals))
	for _, sess := range h.Sessions {
		rows = append(rows, Listed{Session: sess})
	}
	for _, term := range h.Terminals {
		rows = append(rows, Listed{Session: term, Terminal: true})
	}
	rank := make(map[string]int, len(h.Order))
	for i, id := range h.Order {
		if _, named := rank[id]; !named {
			rank[id] = i
		}
	}
	place := func(row Listed) int {
		if i, named := rank[row.ID]; named {
			return i
		}
		return len(h.Order)
	}
	slices.SortStableFunc(rows, func(a, b Listed) int { return place(a) - place(b) })
	return rows
}

func (h Host) rowIDs() []string {
	var ids []string
	for _, row := range h.Rows() {
		ids = append(ids, row.ID)
	}
	return ids
}

// PlaceSession puts a row right before or after a sibling in what host last
// listed, and returns the group and the parent they share.
func (c *Connections) PlaceSession(host, id, anchor string, after bool) (group, parent string, ok bool) {
	state := c.hosts[host]
	rows := state.Rows()
	at := func(id string) int { return slices.IndexFunc(rows, func(row Listed) bool { return row.ID == id }) }
	from, to := at(id), at(anchor)
	if from < 0 || to < 0 || rows[from].Group != rows[to].Group || rows[from].ParentID != rows[to].ParentID {
		return "", "", false
	}
	group, parent = rows[from].Group, rows[from].ParentID
	state.Order = nil
	for _, row := range uirail.PlaceBeside(rows, from, to, after) {
		state.Order = append(state.Order, row.ID)
	}
	c.hosts[host] = state
	return group, parent, true
}

// PlaceGroup puts a group right before or after a sibling in what host last
// listed. siblings names them all as the list shows them, which gives one
// only a session's path names a row to move.
func (c *Connections) PlaceGroup(host, path, anchor string, after bool, siblings []string) bool {
	state := c.hosts[host]
	groups := slices.Clone(state.Groups)
	at := func(path string) int {
		return slices.IndexFunc(groups, func(group Group) bool { return group.Path == path })
	}
	for _, sibling := range siblings {
		if at(sibling) < 0 {
			groups = append(groups, Group{Path: sibling})
		}
	}
	from, to := at(path), at(anchor)
	if from < 0 || to < 0 {
		return false
	}
	state.Groups = uirail.PlaceBeside(groups, from, to, after)
	c.hosts[host] = state
	return true
}

// orderHold is a host's moves still on their way to it. gen counts the calls
// that have started or ended, so a refresh started at another count may have
// read the host from before one of them. untaken says the order shown holds
// a move the host did not take.
type orderHold struct {
	pending int
	gen     uint64
	untaken bool
}

// HoldOrder keeps the order the list shows for host until ReleaseOrder
// answers for the same call.
func (c *Connections) HoldOrder(host string) {
	hold := c.holds[host]
	hold.pending++
	c.hold(host, hold)
}

// ReleaseOrder ends a HoldOrder, with whether the host took the order. The
// next refresh to start brings the host's own.
func (c *Connections) ReleaseOrder(host string, took bool) {
	hold := c.holds[host]
	hold.pending--
	hold.untaken = hold.untaken || !took
	c.hold(host, hold)
}

func (c *Connections) hold(host string, hold orderHold) {
	if c.holds == nil {
		c.holds = map[string]orderHold{}
	}
	hold.gen++
	c.holds[host] = hold
}

// ordered is a refresh of host with the order shown now kept, unless the
// refresh started after every move had reached the host and it answered. A
// failed refresh reports the last good one's rows, which are older than a
// move the host took, and better than one it did not.
func (c *Connections) ordered(host string, fresh Host, started uint64) Host {
	shown, seen := c.hosts[host]
	hold := c.holds[host]
	if !seen {
		return fresh
	}
	if hold.pending == 0 && started == hold.gen && (fresh.OK || hold.untaken) {
		if hold.untaken {
			hold.untaken = false
			c.holds[host] = hold
		}
		return fresh
	}
	fresh.Groups = withImplied(fresh, shown.Groups)
	fresh.Order = keepOrder(fresh.rowIDs(), shown.rowIDs(), func(id string) string { return id })
	fresh.Groups = keepOrder(fresh.Groups, shown.Groups, func(group Group) string { return group.Path })
	return fresh
}

// withImplied is a refresh's groups with those of shown that it names only
// through a row's path or a deeper group's, which a snapshot lists no group
// for.
func withImplied(fresh Host, shown []Group) []Group {
	groups := slices.Clone(fresh.Groups)
	listed := map[string]bool{}
	var paths []string
	for _, group := range groups {
		listed[group.Path] = true
		paths = append(paths, group.Path)
	}
	for _, row := range fresh.Rows() {
		paths = append(paths, row.Group)
	}
	for _, group := range shown {
		if listed[group.Path] || !slices.ContainsFunc(paths, func(path string) bool { return uirail.InGroupSubtree(path, group.Path) }) {
			continue
		}
		listed[group.Path] = true
		groups = append(groups, group)
	}
	return groups
}

// keepOrder puts the items of fresh that shown also has in shown's order, in
// the places they hold between them. The others stay where they are, and so
// does a repeat: what a host lists is not trusted to be unique.
func keepOrder[T any](fresh, shown []T, key func(T) string) []T {
	places := make(map[string]int, len(fresh))
	for i, item := range fresh {
		if _, repeated := places[key(item)]; !repeated {
			places[key(item)] = i
		}
	}
	var from, to []int
	taken := map[string]bool{}
	for _, item := range shown {
		if place, ok := places[key(item)]; ok && !taken[key(item)] {
			taken[key(item)] = true
			from, to = append(from, place), append(to, place)
		}
	}
	slices.Sort(to)
	out := slices.Clone(fresh)
	for i := range to {
		out[to[i]] = fresh[from[i]]
	}
	return out
}
