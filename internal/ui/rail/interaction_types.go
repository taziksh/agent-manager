package rail

import (
	"time"

	"github.com/YoanWai/agent-manager/internal/keybind"
)

type dropKind uint8

const (
	dropInto dropKind = iota + 1
	dropUnder
	dropBefore
)

type dropTarget struct {
	kind  dropKind
	key   string
	group string
	id    string
	label string
}

type autoscrollState struct {
	edge     int
	running  bool
	x, y     int
	anchor   int
	anchored bool
}

// reorderOrigin is where a row on a host was lifted from: right before or
// after target, the sibling next to it, hidden or not. A move puts a row
// beside the one it passes, so retracing the steps past a hidden row would
// leave it on the other side.
type reorderOrigin struct {
	target string
	after  bool
}

type reorderState struct {
	active     bool
	lift       int
	key        string
	dragging   bool
	moved      bool
	offset     int
	origin     reorderOrigin
	drop       *dropTarget
	autoscroll autoscrollState
}

type menuItem struct {
	label  string
	action ActionKind
	danger bool
}

type rowMenu struct {
	active bool
	// held is the press that opened the menu, still down, which runs the
	// entry it is let go over. pressed is the entry a later press landed
	// on, or -1, which runs only if the release lands on it too.
	held             bool
	pressed          int
	key              string
	title            string
	items            []menuItem
	index            int
	anchorX, anchorY int
}

type ActionKind uint8

const (
	NoAction ActionKind = iota
	Quit
	Focus
	Attach
	NewSession
	NewGroup
	Fork
	Revive
	MarkIdle
	ReviveAll
	Restart
	Kill
	KillAll
	Archive
	Restore
	Delete
	Prompt
	CopyReply
	OpenSettings
	Resize
	NewTerminal
	OpenEditor
	RenameAction
	MoveToGroup
	OpenMessages
	OpenHelp
	OpenReview
	CancelEnd
	NewConnection
)

type Intent struct {
	Kind   ActionKind
	Target Selection
}

type MutationKind uint8

const (
	SaveCollapsed MutationKind = iota + 1
	SwapSession
	SwapGroup
	PlaceSession
	PlaceSessionBefore
	MoveGroup
	SwapConnection
)

// Mutation is a caller-shaped persistence request. Root executes it with
// captured concrete services off-loop and calls ApplyMutation on completion.
type Mutation struct {
	Kind          MutationKind
	SessionID     string
	TargetID      string
	Group         string
	ParentID      string
	Path          string
	TargetPath    string
	GroupSiblings []string
	// Host is the connection whose rows a swap moves, empty for this
	// machine's. For SwapConnection it is the connection that moves, and
	// TargetHost the one it trades places with.
	Host       string
	TargetHost string
	// After puts a host's row right after its target rather than before it.
	After         bool
	Collapsed     []string
	HostCollapsed []HostFold
	OffsetDelta   int
	Refresh       bool
	FinishReorder bool
}

// Remote reports whether the mutation changes a host's rows, which that
// host stores, rather than this machine's list.
func (m Mutation) Remote() bool { return m.Host != "" && m.Kind != SwapConnection }

type AutoScrollRequest struct {
	Lift  int
	After time.Duration
}

type AutoScrollTick struct {
	Lift int
}

type KeyContext struct {
	ListKeys       keybind.Table
	ArrowStep      bool
	StepInAttaches bool
	EnterFocuses   bool
}

type MouseContext struct {
	BodyOriginY int
	DividerX    int
	FullWidth   bool
	FullLayout  bool
	Now         time.Time
}
