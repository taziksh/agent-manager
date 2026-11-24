package rail

import (
	"fmt"
	"slices"

	"github.com/YoanWai/agent-manager/internal/keybind"
	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) requestReorder(delta int) Decision {
	row, ok := m.row()
	if !ok {
		return Decision{Consumed: true}
	}
	if row.isRoot() {
		return Decision{Consumed: true, Error: "root stays at the top of the list"}
	}
	request, ok := m.swapRequest(row, delta)
	if !ok {
		edge, what := "top", "group"
		if delta > 0 {
			edge = "bottom"
		}
		switch row.kind {
		case SessionRow:
			what = "session"
		case ConnectionRow:
			what = "connection"
		}
		return Decision{Consumed: true, Error: fmt.Sprintf("%s already at the %s of its level", what, edge)}
	}
	return Decision{Consumed: true, Mutations: []Mutation{request}}
}

func (m Model) swapRequest(row treeRow, delta int) (Mutation, bool) {
	target, ok := m.visibleReorderTarget(row, delta)
	if !ok {
		return Mutation{}, false
	}
	switch row.kind {
	case ConnectionRow:
		return Mutation{Kind: SwapConnection, Host: row.host, TargetHost: target.host, OffsetDelta: delta}, true
	case GroupRow:
		return Mutation{
			Kind:          SwapGroup,
			Host:          row.host,
			Path:          row.group,
			TargetPath:    target.group,
			After:         row.host != "" && delta > 0,
			GroupSiblings: m.knownGroupSiblings(row.host, ParentGroup(row.group)),
			OffsetDelta:   delta,
			Refresh:       row.host == "",
		}, true
	}
	return Mutation{
		Kind:        SwapSession,
		Host:        row.host,
		SessionID:   row.sess.ID,
		TargetID:    target.sess.ID,
		After:       row.host != "" && delta > 0,
		OffsetDelta: delta,
		Refresh:     row.host == "",
	}, true
}

func (m Model) visibleReorderTarget(row treeRow, delta int) (treeRow, bool) {
	step := 1
	if delta < 0 {
		step = -1
	}
	for index := m.cursor + step; index >= 0 && index < len(m.rows); index += step {
		candidate := m.rows[index]
		if row.kind == ConnectionRow {
			if candidate.kind == ConnectionRow {
				return candidate, true
			}
			continue
		}
		if candidate.isRoot() || candidate.host != row.host {
			continue
		}
		if row.kind == GroupRow {
			if candidate.kind == GroupRow && ParentGroup(candidate.group) == ParentGroup(row.group) {
				return candidate, true
			}
			continue
		}
		if candidate.kind == SessionRow && candidate.sess.Group == row.sess.Group && candidate.sess.ParentID == row.sess.ParentID {
			return candidate, true
		}
	}
	return treeRow{}, false
}

func (m Model) knownGroupSiblings(host, parent string) []string {
	groups := m.snapshot.Groups
	if host != "" {
		remote, _ := m.host(host)
		groups = remote.Groups
	}
	return append([]string(nil), childIndex(groupClosure(groups, m.sessionsOf(host)), groups)[parent]...)
}

func (m *Model) ApplyMutation(request Mutation, mutationErr error) Decision {
	if mutationErr != nil {
		return Decision{Consumed: true, Error: mutationErr.Error()}
	}
	switch request.Kind {
	case SaveCollapsed:
		return Decision{Consumed: true}
	case SwapSession, SwapGroup, SwapConnection:
		m.swapLocal(request)
	case PlaceSession, PlaceSessionBefore, MoveGroup:
		if request.Group != "" && m.collapsed[request.Group] {
			m.collapsed[request.Group] = false
			if request.FinishReorder {
				m.finishReorder()
			}
			m.rebuildRows()
			return Decision{Consumed: true, Refresh: request.Refresh, Selection: selectedOf(m), Mutations: []Mutation{m.saveFolds()}}
		}
	}
	if request.OffsetDelta != 0 && m.reorder.active {
		m.reorder.offset += request.OffsetDelta
		m.reorder.moved = true
	}
	if request.FinishReorder {
		m.finishReorder()
	}
	m.rebuildRows()
	return Decision{Consumed: true, Refresh: request.Refresh, Selection: selectedOf(m)}
}

// swapLocal makes a swap in the rail's own copy of the rows it names: this
// machine's, or a host's.
func (m *Model) swapLocal(request Mutation) {
	sessions, groups := &m.snapshot.Sessions, &m.snapshot.Groups
	if request.Kind == SwapConnection {
		swapItems(m.snapshot.Hosts, func(host Host) string { return host.Name }, request.Host, request.TargetHost)
		return
	}
	if request.Host != "" {
		index := slices.IndexFunc(m.snapshot.Hosts, func(host Host) bool { return host.Name == request.Host })
		if index < 0 {
			return
		}
		sessions, groups = &m.snapshot.Hosts[index].Sessions, &m.snapshot.Hosts[index].Groups
	}
	sessionID := func(session Session) string { return session.ID }
	if request.Kind == SwapSession && request.Remote() {
		*sessions = placeItem(*sessions, sessionID, request.SessionID, request.TargetID, request.After)
		return
	}
	if request.Kind == SwapSession {
		swapItems(*sessions, sessionID, request.SessionID, request.TargetID)
		return
	}
	for _, path := range request.GroupSiblings {
		if !slices.Contains(*groups, path) {
			*groups = append(*groups, path)
		}
	}
	groupPath := func(path string) string { return path }
	if request.Remote() {
		*groups = placeItem(*groups, groupPath, request.Path, request.TargetPath, request.After)
		return
	}
	swapItems(*groups, groupPath, request.Path, request.TargetPath)
}

func placeItem[T any](items []T, key func(T) string, id, anchor string, after bool) []T {
	from := slices.IndexFunc(items, func(item T) bool { return key(item) == id })
	to := slices.IndexFunc(items, func(item T) bool { return key(item) == anchor })
	if from < 0 || to < 0 || from == to {
		return items
	}
	return PlaceBeside(items, from, to, after)
}

// PlaceBeside is items with the one at from put right before or after the
// one at to, which is another.
func PlaceBeside[T any](items []T, from, to int, after bool) []T {
	item := items[from]
	rest := slices.Delete(slices.Clone(items), from, from+1)
	if from < to {
		to--
	}
	if after {
		to++
	}
	return slices.Insert(rest, to, item)
}

func swapItems[T any](items []T, key func(T) string, from, to string) {
	current := slices.IndexFunc(items, func(item T) bool { return key(item) == from })
	target := slices.IndexFunc(items, func(item T) bool { return key(item) == to })
	if current >= 0 && target >= 0 {
		items[current], items[target] = items[target], items[current]
	}
}

func (m *Model) enterReorder(key string) {
	m.lifts++
	m.menu = rowMenu{}
	m.reorder = reorderState{active: true, lift: m.lifts, key: key, dragging: true, origin: m.originOf(key)}
}

// originOf is where the row at key sits among its siblings on its host, and
// empty for a row of this machine, whose swaps undo themselves.
func (m Model) originOf(key string) reorderOrigin {
	index := m.rowIndex(key)
	if index < 0 || m.rows[index].host == "" || m.rows[index].kind == ConnectionRow {
		return reorderOrigin{}
	}
	row := m.rows[index]
	id := row.group
	if row.kind == SessionRow {
		id = row.sess.ID
	}
	siblings := m.siblingsOf(row)
	at := slices.Index(siblings, id)
	switch {
	case at > 0:
		return reorderOrigin{target: siblings[at-1], after: true}
	case at == 0 && len(siblings) > 1:
		return reorderOrigin{target: siblings[1]}
	}
	return reorderOrigin{}
}

// siblingsOf names a session or group row's siblings, itself and the hidden
// ones included, by id or path.
func (m Model) siblingsOf(row treeRow) []string {
	if row.kind == GroupRow {
		return m.knownGroupSiblings(row.host, ParentGroup(row.group))
	}
	var siblings []string
	for _, session := range m.sessionsOf(row.host) {
		if session.Group == row.sess.Group && session.ParentID == row.sess.ParentID {
			siblings = append(siblings, session.ID)
		}
	}
	return siblings
}

// originStands reports whether the sibling the lifted row was next to is
// still one, so the row can go back beside it.
func (m Model) originStands() bool {
	row, ok := m.row()
	return ok && m.reorder.origin.target != "" && slices.Contains(m.siblingsOf(row), m.reorder.origin.target)
}

// undoOnHost puts a lifted row of a host back where it was lifted from.
func (m Model) undoOnHost() []Mutation {
	row, ok := m.row()
	if !ok || !m.reorder.moved || row.key() != m.reorder.key {
		return nil
	}
	request := Mutation{Kind: SwapSession, Host: row.host, SessionID: row.sess.ID, TargetID: m.reorder.origin.target}
	if row.kind == GroupRow {
		request = Mutation{
			Kind: SwapGroup, Host: row.host, Path: row.group, TargetPath: m.reorder.origin.target,
			GroupSiblings: m.knownGroupSiblings(row.host, ParentGroup(row.group)),
		}
	}
	request.After, request.OffsetDelta, request.FinishReorder = m.reorder.origin.after, -m.reorder.offset, true
	return []Mutation{request}
}

func (m *Model) finishReorder() { m.reorder = reorderState{} }

// EndLift drops the lifted row where it is. Its host did not take its last
// move, is offline, or no longer lists the two rows of the next one as
// siblings, so the steps esc would undo no longer lead back.
func (m *Model) EndLift() { m.finishReorder() }

// Siblings names rows a reorder arranges on a host: the groups under Group,
// or the sessions in it that share a parent.
type Siblings struct {
	Host          string
	Groups        bool
	Group, Parent string
}

// LiftedAmong reports whether the lifted row is one of siblings.
func (m Model) LiftedAmong(siblings Siblings) bool {
	index := m.rowIndex(m.reorder.key)
	if index < 0 || m.rows[index].host != siblings.Host {
		return false
	}
	row := m.rows[index]
	switch row.kind {
	case GroupRow:
		return siblings.Groups && ParentGroup(row.group) == siblings.Group
	case SessionRow:
		return !siblings.Groups && row.sess.Group == siblings.Group && row.sess.ParentID == siblings.Parent
	}
	return false
}

func (m *Model) reorderKey(msg tea.KeyMsg, ctx KeyContext) Decision {
	key := keybind.Normalize(msg.String())
	action, _ := ctx.ListKeys.ActionFor(key)
	switch {
	case action == keybind.Quit:
		m.finishReorder()
		return m.intent(Quit)
	case key == "enter" || key == "space":
		m.finishReorder()
		return Decision{Consumed: true}
	case key == "esc":
		requests := m.planUndo()
		if len(requests) == 0 {
			m.finishReorder()
		}
		return Decision{Consumed: true, Mutations: requests}
	case key == "up" || action == keybind.Up || action == keybind.ReorderUp:
		return m.stepLifted(-1)
	case key == "down" || action == keybind.Down || action == keybind.ReorderDown:
		return m.stepLifted(1)
	default:
		return Decision{Consumed: true}
	}
}

func (m *Model) stepLifted(delta int) Decision {
	m.reorder.drop = nil
	m.reorder.autoscroll.anchored = false
	row, ok := m.row()
	if !ok || row.key() != m.reorder.key {
		return Decision{Consumed: true}
	}
	request, ok := m.swapRequest(row, delta)
	if !ok {
		return Decision{Consumed: true}
	}
	return Decision{Consumed: true, Mutations: []Mutation{request}}
}

func (m Model) planUndo() []Mutation {
	if m.originStands() {
		return m.undoOnHost()
	}
	scratch := m
	scratch.snapshot = cloneSnapshot(m.snapshot)
	scratch.rows = append([]treeRow(nil), m.rows...)
	var requests []Mutation
	for scratch.reorder.offset != 0 {
		delta := -1
		if scratch.reorder.offset < 0 {
			delta = 1
		}
		row, ok := scratch.row()
		if !ok || row.key() != m.reorder.key {
			break
		}
		request, ok := scratch.swapRequest(row, delta)
		if !ok {
			break
		}
		requests = append(requests, request)
		scratch.applyPlannedSwap(request)
		scratch.reorder.offset += delta
	}
	if len(requests) > 0 {
		requests[len(requests)-1].FinishReorder = true
	}
	return requests
}

func (m *Model) applyPlannedSwap(request Mutation) {
	m.swapLocal(request)
	m.rebuildRows()
}

func (m Model) planDrag(target Selection) []Mutation {
	scratch := m
	scratch.snapshot = cloneSnapshot(m.snapshot)
	scratch.rows = append([]treeRow(nil), m.rows...)
	targetIndex := scratch.rowIndex(target.key())
	var requests []Mutation
	for scratch.cursor != targetIndex && len(requests) < len(scratch.rows) {
		row, ok := scratch.row()
		if !ok || row.key() != m.reorder.key {
			break
		}
		delta := 1
		if targetIndex < scratch.cursor {
			delta = -1
		}
		next, ok := scratch.visibleReorderTarget(row, delta)
		if !ok {
			break
		}
		nextIndex := scratch.rowIndex(next.key())
		if (delta > 0 && nextIndex > targetIndex) || (delta < 0 && nextIndex < targetIndex) {
			break
		}
		request, _ := scratch.swapRequest(row, delta)
		requests = append(requests, request)
		scratch.applyPlannedSwap(request)
	}
	return requests
}

func (m Model) rowIndex(key string) int {
	for index, row := range m.rows {
		if row.key() == key {
			return index
		}
	}
	return -1
}

func (m Model) resolveDrop(target Selection) (dropTarget, bool) {
	if m.showArchived || m.filter != statusFilterAll || m.search != "" {
		return dropTarget{}, false
	}
	liftedIndex, targetIndex := m.rowIndex(m.reorder.key), m.rowIndex(target.key())
	if liftedIndex < 0 || targetIndex < 0 || liftedIndex == targetIndex {
		return dropTarget{}, false
	}
	lifted, targetRow := m.rows[liftedIndex], m.rows[targetIndex]
	if lifted.host != "" || targetRow.host != "" {
		return dropTarget{}, false
	}
	if lifted.kind == GroupRow {
		return resolveGroupDrop(lifted.group, targetRow)
	}
	return m.resolveSessionDrop(lifted.sess, targetRow)
}

func (m Model) resolveSessionDrop(session Session, target treeRow) (dropTarget, bool) {
	if target.kind == GroupRow {
		if target.group == session.Group && session.ParentID == "" {
			return dropTarget{}, false
		}
		return dropTarget{kind: dropInto, key: target.key(), group: target.group, label: "into " + groupLabel(target.group)}, true
	}
	other := target.sess
	if other.ID == session.ParentID || other.ParentID == session.ID {
		return dropTarget{}, false
	}
	if session.IsShell && !other.IsShell && other.ParentID == "" {
		return dropTarget{kind: dropUnder, key: target.key(), group: other.Group, id: other.ID, label: "under " + sessionName(other)}, true
	}
	if !session.IsShell && other.ParentID != "" {
		parent, ok := m.session(other.ParentID)
		if !ok {
			return dropTarget{}, false
		}
		other = parent
	}
	if other.Group == session.Group && other.ParentID == session.ParentID {
		return dropTarget{}, false
	}
	return dropTarget{
		kind: dropBefore, key: Selection{Kind: SessionRow, SessionID: other.ID, Group: other.Group}.key(),
		group: other.Group, id: other.ID, label: "before " + sessionName(other) + " in " + groupLabel(other.Group),
	}, true
}

func resolveGroupDrop(path string, target treeRow) (dropTarget, bool) {
	destination := target.group
	if target.kind == SessionRow {
		destination = target.sess.Group
	} else if !target.isRoot() && ParentGroup(target.group) == ParentGroup(path) {
		return dropTarget{}, false
	}
	if destination == ParentGroup(path) || InGroupSubtree(destination, path) {
		return dropTarget{}, false
	}
	key := Selection{Kind: GroupRow, Group: destination}.key()
	return dropTarget{kind: dropInto, key: key, group: destination, label: "into " + groupLabel(destination)}, true
}

func groupLabel(path string) string {
	if path == "" {
		return "root"
	}
	return BaseName(path)
}

func sessionName(session Session) string {
	if session.DisplayName != "" {
		return session.DisplayName
	}
	return session.Name
}

func (m Model) session(id string) (Session, bool) {
	for _, session := range m.snapshot.Sessions {
		if session.ID == id {
			return session, true
		}
	}
	return Session{}, false
}

func (m *Model) dropMutation(drop dropTarget) (Mutation, bool) {
	index := m.rowIndex(m.reorder.key)
	if index < 0 {
		return Mutation{}, false
	}
	lifted := m.rows[index]
	switch {
	case lifted.kind == GroupRow:
		return Mutation{Kind: MoveGroup, Path: lifted.group, Group: drop.group, TargetPath: drop.group, Refresh: true, FinishReorder: true}, true
	case drop.kind == dropInto:
		return Mutation{Kind: PlaceSession, SessionID: lifted.sess.ID, Group: drop.group, Refresh: true, FinishReorder: true}, true
	case drop.kind == dropUnder:
		return Mutation{Kind: PlaceSession, SessionID: lifted.sess.ID, Group: drop.group, ParentID: drop.id, Refresh: true, FinishReorder: true}, true
	case drop.kind == dropBefore:
		return Mutation{Kind: PlaceSessionBefore, SessionID: lifted.sess.ID, TargetID: drop.id, Group: drop.group, Refresh: true, FinishReorder: true}, true
	default:
		return Mutation{}, false
	}
}
