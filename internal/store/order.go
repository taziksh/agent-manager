package store

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ReorderSession moves a session one visible step among its group
// siblings, reporting whether anything moved. Hidden (archived)
// siblings are skipped when the caller's view excludes them, so a move
// always has a visible effect. Siblings are renumbered to a dense 0..n
// first, since fresh databases start with ties.
func (s *Store) ReorderSession(id string, delta int, includeArchived bool) (bool, error) {
	sess, err := s.Get(id)
	if err != nil {
		return false, err
	}
	rows, err := s.db.Query(
		`SELECT id, archived FROM sessions WHERE group_name = ? AND parent_id = ?
		 ORDER BY sort_order, created_at`, sess.Group, sess.ParentID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	type sibling struct {
		id       string
		archived bool
	}
	var siblings []sibling
	for rows.Next() {
		var sib sibling
		var archived int
		if err := rows.Scan(&sib.id, &archived); err != nil {
			return false, err
		}
		sib.archived = archived != 0
		siblings = append(siblings, sib)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}

	current, target := -1, -1
	for i, sib := range siblings {
		if sib.id == id {
			current = i
			break
		}
	}
	if current < 0 {
		return false, fmt.Errorf("session %s not found among its siblings", id)
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	for i := current + step; i >= 0 && i < len(siblings); i += step {
		if siblings[i].archived && !includeArchived {
			continue
		}
		target = i
		break
	}
	if target < 0 {
		return false, nil
	}

	siblings[current], siblings[target] = siblings[target], siblings[current]
	ids := make([]string, len(siblings))
	for i, sibling := range siblings {
		ids[i] = sibling.id
	}
	if err := s.persistSessionOrder(ids); err != nil {
		return false, err
	}
	return true, nil
}

// SwapSessionOrder exchanges two sessions in the same group. The caller can
// choose visible siblings even when filtered sessions sit between them.
func (s *Store) SwapSessionOrder(id, targetID string) error {
	sess, err := s.Get(id)
	if err != nil {
		return err
	}
	target, err := s.Get(targetID)
	if err != nil {
		return err
	}
	if sess.Group != target.Group || sess.ParentID != target.ParentID {
		return fmt.Errorf("sessions %s and %s are not siblings", id, targetID)
	}

	ids, err := siblingOrder(s.db, sess.Group, sess.ParentID)
	if err != nil {
		return err
	}
	current, targetIndex := slices.Index(ids, id), slices.Index(ids, targetID)
	if current < 0 || targetIndex < 0 {
		return fmt.Errorf("session order changed while reordering")
	}
	ids[current], ids[targetIndex] = ids[targetIndex], ids[current]
	return s.persistSessionOrder(ids)
}

// PlaceSessionBefore moves a session in beside another one, into its group
// and under its parent, and orders it right ahead of it, in one transaction.
func (s *Store) PlaceSessionBefore(id, beforeID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var group, parentID string
	err = tx.QueryRow(`SELECT group_name, parent_id FROM sessions WHERE id = ?`, beforeID).Scan(&group, &parentID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("session %s: %w", beforeID, ErrSessionGone)
	}
	if err != nil {
		return err
	}
	if err := placeSession(tx, id, group, parentID); err != nil {
		return err
	}
	ids, err := siblingOrder(tx, group, parentID)
	if err != nil {
		return err
	}
	ordered := make([]string, 0, len(ids))
	for _, sibling := range ids {
		if sibling == beforeID {
			ordered = append(ordered, id)
		}
		if sibling != id {
			ordered = append(ordered, sibling)
		}
	}
	if err := writeSessionOrder(tx, ordered); err != nil {
		return err
	}
	return tx.Commit()
}

// rowsQuerier is the read half of database/sql, satisfied by both *sql.DB
// and *sql.Tx.
type rowsQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// siblingOrder lists the sessions sharing a group and parent, in the order
// the list shows them.
func siblingOrder(q rowsQuerier, group, parentID string) ([]string, error) {
	rows, err := q.Query(
		`SELECT id FROM sessions WHERE group_name = ? AND parent_id = ?
		 ORDER BY sort_order, created_at`, group, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ErrOrderChanged reports a move beside a row that is gone or no longer a
// sibling, so the list it was made on is stale.
var ErrOrderChanged = errors.New("the list changed since it was read; try again")

// MoveSessionBeside orders a session right before or after another that
// shares its group and parent. The rest keep their order.
func (s *Store) MoveSessionBeside(id, anchorID string, after bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var group, parentID string
	err = tx.QueryRow(`SELECT group_name, parent_id FROM sessions WHERE id = ?`, id).Scan(&group, &parentID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrOrderChanged
	}
	if err != nil {
		return err
	}
	siblings, err := siblingOrder(tx, group, parentID)
	if err != nil {
		return err
	}
	ordered, ok := movedBeside(siblings, id, anchorID, after)
	if !ok {
		return ErrOrderChanged
	}
	if err := writeSessionOrder(tx, ordered); err != nil {
		return err
	}
	return tx.Commit()
}

// MoveGroupBeside orders a group right before or after another under the
// same parent. Siblings only a session's path names get a row first, where
// the list shows them, so they keep their places. Other groups keep theirs.
func (s *Store) MoveGroupBeside(path, anchor string, after bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stored, err := txStrings(tx, `SELECT name FROM groups ORDER BY sort_order, name`)
	if err != nil {
		return err
	}
	filed, err := txStrings(tx, `SELECT DISTINCT group_name FROM sessions ORDER BY group_name`)
	if err != nil {
		return err
	}
	parent := parentPath(path)
	var siblings, implied []string
	for _, name := range stored {
		if parentPath(name) == parent {
			siblings = append(siblings, name)
		}
	}
	for _, name := range append(stored, filed...) {
		for ; name != ""; name = parentPath(name) {
			if parentPath(name) == parent && !slices.Contains(siblings, name) && !slices.Contains(implied, name) {
				implied = append(implied, name)
			}
		}
	}
	slices.Sort(implied)
	ordered, ok := movedBeside(append(siblings, implied...), path, anchor, after)
	if !ok {
		return ErrOrderChanged
	}
	for _, sibling := range ordered {
		if _, err := tx.Exec(
			`INSERT INTO groups (name, sort_order)
			 VALUES (?, (SELECT COALESCE(MAX(sort_order)+1, 0) FROM groups))
			 ON CONFLICT(name) DO NOTHING`, sibling); err != nil {
			return err
		}
	}
	stored, err = txStrings(tx, `SELECT name FROM groups ORDER BY sort_order, name`)
	if err != nil {
		return err
	}
	next := 0
	for i, name := range stored {
		if slices.Contains(ordered, name) {
			name = ordered[next]
			next++
		}
		if _, err := tx.Exec(`UPDATE groups SET sort_order = ? WHERE name = ?`, i, name); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// movedBeside is order with id taken out and put right before or after
// anchor; ok is false unless order holds both.
func movedBeside(order []string, id, anchor string, after bool) (moved []string, ok bool) {
	if id == anchor || !slices.Contains(order, id) || !slices.Contains(order, anchor) {
		return nil, false
	}
	for _, each := range order {
		switch {
		case each == anchor && after:
			moved = append(moved, anchor, id)
		case each == anchor:
			moved = append(moved, id, anchor)
		case each != id:
			moved = append(moved, each)
		}
	}
	return moved, true
}

func (s *Store) persistSessionOrder(ids []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := writeSessionOrder(tx, ids); err != nil {
		return err
	}
	return tx.Commit()
}

func writeSessionOrder(ex executor, ids []string) error {
	for i, id := range ids {
		if _, err := ex.Exec(`UPDATE sessions SET sort_order = ? WHERE id = ?`, i, id); err != nil {
			return err
		}
	}
	return nil
}

// ReorderGroup moves a group one step among the groups sharing its
// parent path, reporting whether anything moved. All groups are
// renumbered to their current global order first so sibling swaps are
// well-defined.
func (s *Store) ReorderGroup(path string, delta int) (bool, error) {
	if path == "" {
		return false, fmt.Errorf("cannot reorder the root group")
	}
	if err := s.ensureGroup(path); err != nil {
		return false, err
	}
	groups, err := s.Groups()
	if err != nil {
		return false, err
	}
	parent := ""
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		parent = path[:idx]
	}
	isSibling := func(name string) bool {
		if parent == "" {
			return !strings.Contains(name, "/")
		}
		rest, ok := strings.CutPrefix(name, parent+"/")
		return ok && !strings.Contains(rest, "/")
	}

	current, target := -1, -1
	for i, g := range groups {
		if g.Name == path {
			current = i
			break
		}
	}
	if current < 0 {
		return false, fmt.Errorf("group %s not found", path)
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	for i := current + step; i >= 0 && i < len(groups); i += step {
		if isSibling(groups[i].Name) {
			target = i
			break
		}
	}
	if target < 0 {
		return false, nil
	}

	groups[current], groups[target] = groups[target], groups[current]
	if err := s.persistGroupOrder(groups); err != nil {
		return false, err
	}
	return true, nil
}

// SwapGroupOrder exchanges two groups with the same parent. siblingOrder
// materializes displayed ancestors so their manual order can persist too.
func (s *Store) SwapGroupOrder(path, targetPath string, siblingOrder ...string) error {
	if path == "" || targetPath == "" {
		return fmt.Errorf("cannot reorder the root group")
	}
	parent := parentPath(path)
	if parent != parentPath(targetPath) {
		return fmt.Errorf("groups %s and %s are not siblings", path, targetPath)
	}
	for _, sibling := range siblingOrder {
		if parentPath(sibling) != parent {
			return fmt.Errorf("group %s is not a sibling of %s", sibling, path)
		}
		if err := s.ensureGroup(sibling); err != nil {
			return err
		}
	}
	groups, err := s.Groups()
	if err != nil {
		return err
	}
	current, target := -1, -1
	for i, group := range groups {
		switch group.Name {
		case path:
			current = i
		case targetPath:
			target = i
		}
	}
	if current < 0 || target < 0 {
		return fmt.Errorf("group order changed while reordering")
	}
	groups[current], groups[target] = groups[target], groups[current]
	return s.persistGroupOrder(groups)
}

func parentPath(path string) string {
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[:idx]
	}
	return ""
}

func (s *Store) persistGroupOrder(groups []Group) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, g := range groups {
		if _, err := tx.Exec(`UPDATE groups SET sort_order = ? WHERE name = ?`, i, g.Name); err != nil {
			return err
		}
	}
	return tx.Commit()
}
