package sessioncmd

import (
	"fmt"
	"slices"
	"strings"

	"github.com/YoanWai/agent-manager/internal/store"
)

// refuseReorderCaller keeps the order of the list the user's own, the way
// the reorder keys do.
func refuseReorderCaller(sessionID string) error {
	if sessionID == "" {
		return nil
	}
	return fmt.Errorf("reordering is for the user's reorder keys on another machine; session %s cannot change the order of the list", sessionID)
}

// ReorderSession puts an agent or a terminal right before or after another
// that shares its group and parent, as the reorder keys do.
func (s *Sessions) ReorderSession(sessionID, targetID, anchorID string, after bool) (Session, error) {
	if err := refuseReorderCaller(sessionID); err != nil {
		return Session{}, err
	}
	runtime, err := s.open()
	if err != nil {
		return Session{}, err
	}
	defer runtime.Close()
	if err := runtime.store.MoveSessionBeside(targetID, anchorID, after); err != nil {
		return Session{}, err
	}
	moved, err := runtime.store.Get(targetID)
	if err != nil {
		return Session{}, fmt.Errorf("reordered %s, but could not read it back: %w", targetID, err)
	}
	return runtime.sessionInfo(moved, runtime.driver.Exists(moved.ID), false), nil
}

// ReorderGroup puts a group right before or after another under the same
// parent, as ReorderSession does a session.
func (s *Sessions) ReorderGroup(sessionID, path, anchor string, after bool) (Group, error) {
	if err := refuseReorderCaller(sessionID); err != nil {
		return Group{}, err
	}
	path, anchor = strings.Trim(strings.TrimSpace(path), "/"), strings.Trim(strings.TrimSpace(anchor), "/")
	runtime, err := s.open()
	if err != nil {
		return Group{}, err
	}
	defer runtime.Close()
	if err := runtime.store.MoveGroupBeside(path, anchor, after); err != nil {
		return Group{}, err
	}
	groups, err := runtime.store.Groups()
	if err != nil {
		return Group{}, fmt.Errorf("reordered %s, but could not read it back: %w", path, err)
	}
	filed, err := runtime.store.ListSessions(true)
	if err != nil {
		return Group{}, fmt.Errorf("reordered %s, but could not read it back: %w", path, err)
	}
	at := slices.IndexFunc(groups, func(group store.Group) bool { return group.Name == path })
	if at < 0 {
		return Group{}, fmt.Errorf("reordered %s, but it is gone", path)
	}
	return runtime.groupRows(groups[at:at+1], filed)[0], nil
}
