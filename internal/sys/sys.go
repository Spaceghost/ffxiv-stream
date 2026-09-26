// Package sys runs commands and touches files for the rest of xivstream,
// in one place, so dry runs and tests can see every side effect.
package sys

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Output runs argv and returns its trimmed stdout. stderr is folded into the
// error so a failure says why.
func Output(argv ...string) (string, error) {
	return OutputTimeout(2*time.Minute, argv...)
}

func OutputTimeout(timeout time.Duration, argv ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = out
		}
		if len(msg) > 800 {
			msg = "..." + msg[len(msg)-800:]
		}
		return out, fmt.Errorf("%s: %w: %s", strings.Join(argv, " "), err, msg)
	}
	return out, nil
}

// Stream runs argv with its output on ours (long installs show progress).
func Stream(argv ...string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, nil
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	return nil
}

// Has reports whether a command is on PATH.
func Has(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// Exists reports whether a path exists.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ReadTrim reads a small file (sysfs, /etc/os-release), trimmed; "" on error.
func ReadTrim(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// Quote renders argv for display, shell-style.
func Quote(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if a == "" || strings.ContainsAny(a, " \t\n'\"$`\\|&;<>()*?[]#~") {
			parts[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}
