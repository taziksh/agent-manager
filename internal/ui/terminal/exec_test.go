package terminal

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestExecProcessKeepsChildStdoutOnTheTerminal(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "sh", "-c", "true")
	ExecProcess(cmd, nil)
	if cmd.Stdout != os.Stdout {
		t.Fatal("interactive child stdout was not pinned to the terminal")
	}
}

func TestExecProcessPreservesConfiguredStdout(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("close temporary stdout file: %v", err)
		}
	}()

	cmd := exec.CommandContext(context.Background(), "sh", "-c", "true")
	cmd.Stdout = file
	ExecProcess(cmd, nil)
	if cmd.Stdout != file {
		t.Fatal("preconfigured child stdout was overwritten")
	}
}

type childRun struct {
	stdout *os.File
	err    error
}

func (c childRun) Run() error {
	if _, err := c.stdout.WriteString("child"); err != nil {
		return err
	}
	return c.err
}

func (childRun) SetStdin(io.Reader)  {}
func (childRun) SetStdout(io.Writer) {}
func (childRun) SetStderr(io.Writer) {}

type execRecorder struct {
	child tea.ExecCommand
	done  bool
	err   error
}

type execDoneMsg struct{ err error }

func (r *execRecorder) Init() tea.Cmd {
	return Exec(r.child, func(err error) tea.Msg { return execDoneMsg{err} })
}

func (r *execRecorder) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if done, ok := msg.(execDoneMsg); ok {
		r.done, r.err = true, done.err
		return r, tea.Quit
	}
	return r, nil
}

func (r *execRecorder) View() string { return "" }

// A child owns the terminal with reports off, so a flip never reaches it as
// typed input. The callback asks for the scheme after Bubble Tea restores
// the terminal, since turning reports back on does not make it report.
func TestExecPausesSchemeReportsAroundChild(t *testing.T) {
	for _, tt := range []struct {
		name  string
		child error
	}{
		{"child succeeds", nil},
		{"child fails", errors.New("exit status 1")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			read, write, err := os.Pipe()
			if err != nil {
				t.Fatalf("pipe: %v", err)
			}
			saved := os.Stdout
			os.Stdout = write
			recorder := &execRecorder{child: childRun{stdout: write, err: tt.child}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, runErr := tea.NewProgram(recorder, tea.WithInput(strings.NewReader("")), tea.WithOutput(io.Discard),
				tea.WithoutRenderer(), tea.WithContext(ctx)).Run()
			os.Stdout = saved
			if err := write.Close(); err != nil {
				t.Fatalf("close write end: %v", err)
			}
			if runErr != nil {
				t.Fatalf("run: %v", runErr)
			}
			if !recorder.done || !errors.Is(recorder.err, tt.child) {
				t.Fatalf("callback done=%v err=%v, want the child's %v", recorder.done, recorder.err, tt.child)
			}
			got, err := io.ReadAll(read)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			want := "child"
			if schemeReports {
				want = ansi.ResetModeLightDark + "child" + ansi.SetModeLightDark + ansi.RequestLightDarkReport
			}
			if string(got) != want {
				t.Fatalf("emitted %q, want %q", got, want)
			}
		})
	}
}
