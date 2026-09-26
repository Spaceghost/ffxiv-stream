package plan

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/Spaceghost/xivstream-dalamud/internal/sys"
)

// Target is a machine steps act on: this one, or an Incus container on it.
type Target interface {
	Name() string
	Run(argv ...string) (string, error)
	// RunInput runs argv with stdin (a script, a file body).
	RunInput(stdin []byte, argv ...string) (string, error)
	ReadFile(path string) ([]byte, error)
	// WriteFile writes atomically; owner is "user:group" or "" (unchanged/root).
	WriteFile(path string, data []byte, mode os.FileMode, owner string) error
	Exists(path string) bool
}

// Local is this machine.
type Local struct{}

func (Local) Name() string                       { return "this machine" }
func (Local) Run(argv ...string) (string, error) { return sys.OutputTimeout(30*time.Minute, argv...) }

func (Local) RunInput(stdin []byte, argv ...string) (string, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = bytes.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%s: %w: %s", sys.Quote(argv), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

func (Local) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

func (l Local) WriteFile(path string, data []byte, mode os.FileMode, owner string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if owner != "" {
		// As for Incus: the directories between the file and the owner's home
		// belong to the owner.
		if u, err := user.Lookup(strings.SplitN(owner, ":", 2)[0]); err == nil {
			for d := filepath.Dir(path); strings.HasPrefix(d, u.HomeDir+string(filepath.Separator)); d = filepath.Dir(d) {
				if _, err := l.Run("chown", owner, d); err != nil {
					return err
				}
			}
		}
	}
	tmp := path + ".xivstream.tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil { // WriteFile's mode is masked by the umask
		return err
	}
	if owner != "" {
		if _, err := l.Run("chown", owner, tmp); err != nil {
			return err
		}
	}
	return os.Rename(tmp, path)
}

func (Local) Exists(path string) bool { return sys.Exists(path) }

// Incus is a container, driven through the incus CLI on this host.
type Incus struct{ Container string }

func (c Incus) Name() string { return "container " + c.Container }

func (c Incus) Run(argv ...string) (string, error) {
	return sys.OutputTimeout(30*time.Minute, append([]string{"incus", "exec", c.Container, "--"}, argv...)...)
}

func (c Incus) RunInput(stdin []byte, argv ...string) (string, error) {
	return Local{}.RunInput(stdin, append([]string{"incus", "exec", c.Container, "--"}, argv...)...)
}

func (c Incus) ReadFile(path string) ([]byte, error) {
	out, err := sys.OutputTimeout(time.Minute, "incus", "file", "pull", c.Container+path, "-")
	if err != nil {
		return nil, os.ErrNotExist
	}
	return []byte(out + "\n"), nil
}

func (c Incus) WriteFile(path string, data []byte, mode os.FileMode, owner string) error {
	// Through the container's own shell rather than `incus file push`, so the
	// write is atomic and ownership uses the container's user names.
	// A file in the owner's home gets every directory between it and the home
	// owned by them too (mkdir as root would leave ~/.config root's).
	script := `set -e; d=$(dirname "$1"); mkdir -p "$d"
if [ -n "$3" ]; then
    h=$(getent passwd "${3%%:*}" | cut -d: -f6)
    case "$d/" in "$h"/*) p="$d"; while [ "$p" != "$h" ] && [ "$p" != / ]; do chown "$3" "$p"; p=$(dirname "$p"); done;; esac
fi
t="$1.xivstream.tmp"; cat > "$t"; chmod "$2" "$t"; [ -z "$3" ] || chown "$3" "$t"; mv "$t" "$1"`
	_, err := c.RunInput(data, "sh", "-c", script, "-", path, fmt.Sprintf("%o", mode), owner)
	return err
}

func (c Incus) Exists(path string) bool {
	_, err := c.Run("test", "-e", path)
	return err == nil
}
