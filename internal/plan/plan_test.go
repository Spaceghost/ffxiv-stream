package plan

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestApplySkipsDoneAndStopsAtFailure(t *testing.T) {
	var ran []string
	mk := func(name string, done bool, fail bool) Step {
		return Step{Title: name, Check: func() (bool, error) { return done, nil }, Apply: func() error {
			ran = append(ran, name)
			if fail {
				return errors.New("boom")
			}
			return nil
		}}
	}
	var out bytes.Buffer
	err := Apply(&out, []Step{mk("a", true, false), mk("b", false, false), mk("c", false, true), mk("d", false, false)})
	if err == nil || !strings.Contains(err.Error(), "step 3 (c)") {
		t.Fatalf("err = %v", err)
	}
	if strings.Join(ran, ",") != "b,c" {
		t.Fatalf("ran %v: done steps are skipped, nothing runs after a failure", ran)
	}
}

func TestEvaluateAndPrint(t *testing.T) {
	steps := []Step{
		{Title: "done", Check: func() (bool, error) { return true, nil }},
		{Title: "todo", Shows: []string{"echo hi"}, Check: func() (bool, error) { return false, nil }},
		{Title: "unknown", Check: func() (bool, error) { return false, errors.New("no container") }},
	}
	var out bytes.Buffer
	Print(&out, Evaluate(steps), true)
	s := out.String()
	for _, want := range []string{"[x]  1. done", "[ ]  2. todo", "$ echo hi", "[?]  3. unknown", "2 of 3 steps to do"} {
		if !strings.Contains(s, want) {
			t.Errorf("plan lacks %q:\n%s", want, s)
		}
	}
}

func TestLineDiff(t *testing.T) {
	d := LineDiff("a\nb\nc\n", "a\nB\nc\nd\n")
	if d != "- b\n+ B\n+ d\n" {
		t.Fatalf("diff:\n%q", d)
	}
	if LineDiff("same\n", "same") != "" {
		t.Fatal("no diff for equal content")
	}
}

func TestIfChangedRunsOnlyAfterAChange(t *testing.T) {
	ran := 0
	restart := Step{Title: "restart", IfChanged: true, Apply: func() error { ran++; return nil }}
	done := Step{Title: "done", Check: func() (bool, error) { return true, nil }, Apply: func() error { return nil }}
	todo := Step{Title: "todo", Check: func() (bool, error) { return false, nil }, Apply: func() error { return nil }}
	var out bytes.Buffer
	_ = Apply(&out, []Step{done, restart})
	if ran != 0 {
		t.Fatal("nothing changed: no restart")
	}
	_ = Apply(&out, []Step{todo, restart})
	if ran != 1 {
		t.Fatal("a change: restart")
	}
}
