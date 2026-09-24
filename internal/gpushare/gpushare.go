// Package gpushare gives the game the GPU's memory while it runs
// (`ffxiv-stream gpu-share`, a long-running service).
//
// Two ways, per [gpu_share] mode:
//
//   - reserve: an almanac gateway on the same card takes VRAM reservations
//     (PUT /v1/almanac/gpu/reservations/<owner>). While the game runs this
//     holds one sized from the game's own measured use (the card's usage less
//     almanac's models, at its highest seen, times margin), refreshed with a
//     TTL so it lapses by itself if this service dies. almanac unloads a model
//     that no longer fits at once and serves a smaller one; the big one comes
//     back after the game.
//   - stop-unit: for model servers without such an API (a bare Ollama), the
//     unit is stopped while the game runs and started again afterwards.
//
// Either way the room is given back GraceSeconds after the game exits, so a
// crash-and-relaunch does not bounce the model.
package gpushare

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Spaceghost/ffxiv-stream/internal/config"
	"github.com/Spaceghost/ffxiv-stream/internal/sys"
)

type Sharer struct {
	Cfg config.Share
	// Process is the game's process name; GameContainer, if set, restricts
	// the match to that Incus container's processes (seen in the host /proc).
	Process       string
	GameContainer string
	Poll          time.Duration
	Reassert      time.Duration
	PeakFile      string
	running       func() bool
}

func New(c config.Config) *Sharer {
	s := &Sharer{
		Cfg: c.Share, Process: c.Game.Process, Poll: time.Second, Reassert: 15 * time.Second,
		PeakFile: filepath.Join(config.StateDir(), "game_peak_mb"),
	}
	if c.Topology == config.TopologyIncus {
		s.GameContainer = c.Incus.Container
	}
	s.running = func() bool { return GameRunning(s.Process, s.GameContainer) }
	return s
}

// Run loops forever (the service's main).
func (s *Sharer) Run() error {
	if s.Cfg.Mode == config.ShareNone {
		log.Print("gpu_share.mode = none: nothing to do")
		select {}
	}
	grace := time.Duration(s.Cfg.GraceSeconds) * time.Second
	peak := s.readPeak()
	held := false
	var absent time.Duration
	since := s.Reassert
	for {
		if s.running() {
			absent = 0
			if since >= s.Reassert {
				peak = s.take(peak)
				held, since = true, 0
			}
			since += s.Poll
		} else if held {
			absent += s.Poll
			if absent >= grace && s.giveBack() {
				held, absent, since = false, 0, s.Reassert
			}
		}
		time.Sleep(s.Poll)
	}
}

// Stop is for the service manager's stop hook: give the room back unless the
// game still needs it (a restart of this service then re-takes it).
func (s *Sharer) Stop() {
	if s.Cfg.Mode != config.ShareNone && !s.running() {
		s.giveBack()
	}
}

func (s *Sharer) take(peak int) int {
	switch s.Cfg.Mode {
	case config.ShareReserve:
		return s.reserve(peak)
	case config.ShareStopUnit:
		if s.unitActive() {
			log.Printf("game running: stopping %s%s", s.Cfg.Unit, s.where())
			if _, err := s.inContainer("systemctl", "stop", s.Cfg.Unit); err != nil {
				log.Print(err)
			}
		}
	}
	return peak
}

func (s *Sharer) giveBack() bool {
	switch s.Cfg.Mode {
	case config.ShareReserve:
		var result map[string]any
		if err := s.gateway("DELETE", "/v1/almanac/gpu/reservations/"+s.Cfg.Owner, nil, &result); err != nil {
			log.Printf("releasing the reservation: %v", err)
			return false
		}
		if result["released"] == true {
			log.Print("game gone: reservation released")
		}
	case config.ShareStopUnit:
		log.Printf("game gone: starting %s%s", s.Cfg.Unit, s.where())
		if _, err := s.inContainer("systemctl", "start", s.Cfg.Unit); err != nil {
			log.Print(err)
			return false
		}
	}
	return true
}

type gpuReport struct {
	Inference *struct {
		UsedMB int `json:"used_mb"`
	} `json:"inference"`
	Loaded []struct {
		VRAMMB int `json:"vram_mb"`
	} `json:"loaded"`
	Reservations map[string]int `json:"reservations"`
}

// GameMB is the card's usage less almanac's models: the game, and its
// compositor and encoder.
func GameMB(r gpuReport) (int, bool) {
	if r.Inference == nil {
		return 0, false
	}
	mb := r.Inference.UsedMB
	for _, m := range r.Loaded {
		mb -= m.VRAMMB
	}
	return max(0, mb), true
}

func (s *Sharer) reserve(peak int) int {
	var report gpuReport
	if err := s.gateway("GET", "/v1/almanac/gpu", nil, &report); err != nil {
		log.Printf("cannot reach almanac's gateway%s: %v; retrying", s.where(), err)
		return peak
	}
	now, ok := GameMB(report)
	if ok && now > peak {
		peak = now
		s.writePeak(peak)
	}
	mb := int(float64(max(peak, now)) * s.Cfg.Margin)
	if mb == 0 {
		return peak
	}
	var result struct {
		Auto *struct {
			Choice string `json:"choice"`
		} `json:"auto"`
	}
	ttl := s.Reassert.Seconds() * 4
	if err := s.gateway("PUT", "/v1/almanac/gpu/reservations/"+s.Cfg.Owner, map[string]any{"mb": mb, "ttl_s": ttl}, &result); err != nil {
		log.Printf("reserving: %v", err)
		return peak
	}
	if report.Reservations[s.Cfg.Owner] != mb {
		choice := "?"
		if result.Auto != nil {
			choice = result.Auto.Choice
		}
		log.Printf("game running (using %d MB, peak %d MB): reserved %d MB; almanac serves %s", now, peak, mb, choice)
	}
	return peak
}

// gateway calls almanac. Inside its container when it has one (the gateway
// listens on that container's loopback, and the token lives there too);
// directly over HTTP otherwise.
func (s *Sharer) gateway(method, path string, body any, out any) error {
	url := strings.TrimRight(s.Cfg.Gateway, "/") + path
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body) // compact: no spaces, $4 below is unquoted
	}
	var stdout []byte
	if s.Cfg.Container != "" {
		script := `curl -sf -m 30 -X "$1" -H "authorization: Bearer $(cat "$2")" -H content-type:application/json "$3" ${4:+--data-binary "$4"}`
		argv := []string{"sh", "-c", script, "-", method, s.Cfg.TokenFile, url}
		if data != nil {
			argv = append(argv, string(data))
		}
		got, err := s.inContainer(argv...)
		if err != nil {
			return err
		}
		stdout = []byte(got)
	} else {
		token, err := os.ReadFile(s.Cfg.TokenFile)
		if err != nil {
			return err
		}
		req, err := http.NewRequest(method, url, bytes.NewReader(data))
		if err != nil {
			return err
		}
		req.Header.Set("authorization", "Bearer "+strings.TrimSpace(string(token)))
		req.Header.Set("content-type", "application/json")
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		stdout, _ = io.ReadAll(resp.Body)
		if resp.StatusCode >= 300 {
			return fmt.Errorf("%s %s: %s: %s", method, url, resp.Status, bytes.TrimSpace(stdout))
		}
	}
	if out != nil && len(stdout) > 0 {
		return json.Unmarshal(stdout, out)
	}
	return nil
}

func (s *Sharer) inContainer(argv ...string) (string, error) {
	if s.Cfg.Container != "" {
		argv = append([]string{"incus", "exec", s.Cfg.Container, "--"}, argv...)
	}
	return sys.OutputTimeout(time.Minute, argv...)
}

func (s *Sharer) unitActive() bool {
	_, err := s.inContainer("systemctl", "is-active", "-q", s.Cfg.Unit)
	return err == nil
}

func (s *Sharer) where() string {
	if s.Cfg.Container != "" {
		return " in " + s.Cfg.Container
	}
	return ""
}

func (s *Sharer) readPeak() int {
	n, _ := strconv.Atoi(sys.ReadTrim(s.PeakFile))
	return n
}

func (s *Sharer) writePeak(mb int) {
	if err := os.MkdirAll(filepath.Dir(s.PeakFile), 0o755); err != nil {
		return
	}
	tmp := s.PeakFile + ".tmp"
	if os.WriteFile(tmp, []byte(strconv.Itoa(mb)+"\n"), 0o644) == nil {
		_ = os.Rename(tmp, s.PeakFile)
	}
}

// GameRunning reports whether a process named name runs, in the named Incus
// container when container is set (Linux hosts see container processes in
// their own /proc; the cgroup path names the container).
func GameRunning(name, container string) bool {
	switch runtime.GOOS {
	case "linux":
		return procRunning("/proc", name, container)
	case "windows":
		out, err := sys.Output("tasklist", "/FI", "IMAGENAME eq "+name, "/FO", "CSV", "/NH")
		return err == nil && strings.Contains(strings.ToLower(out), strings.ToLower(name))
	default:
		return exec.Command("pgrep", "-if", name).Run() == nil
	}
}

func procRunning(proc, name, container string) bool {
	want := strings.ToLower(name)
	entries, _ := os.ReadDir(proc)
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(proc, e.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		if !matchesArgv(bytes.Split(bytes.TrimRight(raw, "\x00"), []byte{0}), want) {
			continue
		}
		if container == "" {
			return true
		}
		cg, _ := os.ReadFile(filepath.Join(proc, e.Name(), "cgroup"))
		if bytes.Contains(cg, []byte("/lxc.payload."+container+"/")) {
			return true
		}
	}
	return false
}

// matchesArgv: argv[0]'s basename (Windows paths as Wine shows them, too),
// or argv[1] under a Wine loader.
func matchesArgv(argv [][]byte, want string) bool {
	base := func(b []byte) string {
		s := strings.ReplaceAll(string(b), `\`, "/")
		return strings.ToLower(s[strings.LastIndex(s, "/")+1:])
	}
	if len(argv) == 0 {
		return false
	}
	first := base(argv[0])
	if first == want {
		return true
	}
	if strings.HasPrefix(first, "wine") && len(argv) > 1 {
		return base(argv[1]) == want
	}
	return false
}

func (s *Sharer) String() string {
	return fmt.Sprintf("gpu-share mode=%s process=%s container=%q", s.Cfg.Mode, s.Process, s.GameContainer)
}
