// Package plan is how ffxiv-stream changes a machine: as a list of steps, each
// of which can say whether it is already done, what it would do, and do it.
//
// `ffxiv-stream plan` prints the steps with their state (a dry run, nothing is
// changed); `ffxiv-stream apply` does the ones not done yet. Every step is
// idempotent, so apply can be re-run after a failure, a config change or an
// upgrade and only does what is needed.
package plan

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Spaceghost/ffxiv-stream/internal/sys"
)

type Step struct {
	Title string // one line: what the step achieves
	Why   string // why it is needed (shown in plan)
	On    Target
	// Shows: the commands and files the step touches, for the plan.
	Shows []string
	// Check reports whether the step is already done. nil = always apply.
	Check func() (bool, error)
	Apply func() error
	// Diff, if set, shows what would change (a file's old vs new content).
	Diff func() string
	// IfChanged: run only when an earlier step in this apply did something
	// (restarting a service after its files changed).
	IfChanged bool
}

// File writes a file when its content or mode differs.
func File(on Target, path string, content []byte, mode os.FileMode, owner, title, why string) Step {
	return Step{
		Title: title, Why: why, On: on,
		Shows: []string{fmt.Sprintf("write %s (%d bytes, mode %o%s)", path, len(content), mode, ownerNote(owner))},
		Check: func() (bool, error) {
			old, err := on.ReadFile(path)
			if err != nil {
				return false, nil
			}
			return bytes.Equal(bytes.TrimRight(old, "\n"), bytes.TrimRight(content, "\n")), nil
		},
		Apply: func() error { return on.WriteFile(path, content, mode, owner) },
		Diff: func() string {
			old, err := on.ReadFile(path)
			if err != nil {
				return "(new file)\n" + string(content)
			}
			return LineDiff(string(old), string(content))
		},
	}
}

func ownerNote(owner string) string {
	if owner == "" {
		return ""
	}
	return ", owner " + owner
}

// Command runs argv when check says it is not done yet.
func Command(on Target, title, why string, check func() (bool, error), argv ...string) Step {
	return Step{
		Title: title, Why: why, On: on, Shows: []string{sys.Quote(argv)}, Check: check,
		Apply: func() error { _, err := on.Run(argv...); return err },
	}
}

// State of a step in a plan.
type State int

const (
	Pending State = iota
	Done
	Unknown // its check failed (e.g. the container does not exist yet)
)

type Entry struct {
	Step  Step
	State State
	Err   error
}

// Evaluate checks every step without changing anything.
func Evaluate(steps []Step) []Entry {
	entries := make([]Entry, len(steps))
	for i, s := range steps {
		entries[i].Step = s
		if s.IfChanged { // follows other steps; nothing to do on its own
			entries[i].State = Done
			continue
		}
		if s.Check == nil {
			continue
		}
		done, err := s.Check()
		switch {
		case err != nil:
			entries[i].State, entries[i].Err = Unknown, err
		case done:
			entries[i].State = Done
		}
	}
	return entries
}

// Print renders a plan for humans. verbose adds the commands and diffs.
func Print(w io.Writer, entries []Entry, verbose bool) {
	pending := 0
	for i, e := range entries {
		mark := "[ ]"
		switch e.State {
		case Done:
			mark = "[x]"
		case Unknown:
			mark = "[?]"
		}
		if e.State != Done {
			pending++
		}
		fmt.Fprintf(w, "%s %2d. %s", mark, i+1, e.Step.Title)
		if e.Step.On != nil {
			fmt.Fprintf(w, "  (%s)", e.Step.On.Name())
		}
		fmt.Fprintln(w)
		if !verbose {
			continue
		}
		if e.Step.Why != "" {
			fmt.Fprintf(w, "        why: %s\n", e.Step.Why)
		}
		for _, s := range e.Step.Shows {
			fmt.Fprintf(w, "        $ %s\n", s)
		}
		if e.State != Done && e.Step.Diff != nil {
			if d := strings.TrimRight(e.Step.Diff(), "\n"); d != "" {
				for _, line := range strings.Split(d, "\n") {
					fmt.Fprintf(w, "        | %s\n", line)
				}
			}
		}
	}
	fmt.Fprintf(w, "\n%d of %d steps to do ([x] done, [ ] to do, [?] decided when earlier steps have run)\n", pending, len(entries))
}

// Apply runs the steps in order, skipping those already done, and stops at the
// first failure (apply can then be run again once it is fixed).
func Apply(w io.Writer, steps []Step) error {
	changed := false
	for i, s := range steps {
		if s.IfChanged && !changed {
			fmt.Fprintf(w, "[-] %2d. %s (nothing changed)\n", i+1, s.Title)
			continue
		}
		if s.Check != nil {
			if done, err := s.Check(); err == nil && done {
				fmt.Fprintf(w, "[x] %2d. %s\n", i+1, s.Title)
				continue
			}
		}
		fmt.Fprintf(w, "[>] %2d. %s ... ", i+1, s.Title)
		if err := s.Apply(); err != nil {
			fmt.Fprintln(w, "failed")
			return fmt.Errorf("step %d (%s): %w", i+1, s.Title, err)
		}
		fmt.Fprintln(w, "done")
		changed = true
	}
	return nil
}

// LineDiff is a small unified-style diff: enough to review config changes.
func LineDiff(old, new string) string {
	a, b := strings.Split(strings.TrimRight(old, "\n"), "\n"), strings.Split(strings.TrimRight(new, "\n"), "\n")
	// Longest common subsequence table; the files involved are small.
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out strings.Builder
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			i, j = i+1, j+1
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
			out.WriteString("- " + a[i] + "\n")
			i++
		default:
			out.WriteString("+ " + b[j] + "\n")
			j++
		}
	}
	return out.String()
}
