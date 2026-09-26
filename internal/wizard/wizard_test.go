package wizard

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/Spaceghost/xivstream-dalamud/internal/config"
	"github.com/Spaceghost/xivstream-dalamud/internal/detect"
)

// The wizard in accessible mode, answered line by line as a person (or a
// script) would, against a fake plugin repository.
func TestWizardAccessible(t *testing.T) {
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"InternalName":"GhosttyDalamud","Name":"Ghostty","AssemblyVersion":"1"},
			{"InternalName":"Almanac","Name":"Almanac","AssemblyVersion":"1"},{"InternalName":"XivMcp","Name":"XivMcp","AssemblyVersion":"1"}]`))
	}))
	defer repo.Close()
	t.Setenv("ACCESSIBLE", "1")
	lines := []string{
		"1", // topology: the only option offered (host; no Incus in the fake facts)
		"1", // backend: sunshine
	}
	if runtime.GOOS == "linux" {
		lines = append(lines, "") // session user (asked on Linux only): empty keeps the default
	}
	answers := strings.Join(append(lines,
		"3",      // resolution: 1920x1080@120 (sorted list: 720, 800, 1080@120, 1080@60, ...)
		"4",      // bitrate: 80 Mbit/s
		"1",      // listen: Tailscale
		"n",      // sound stays local
		"1",      // GPU sharing: almanac reserve
		"2", "0", // mods: Almanac, confirm
		"y", // testing versions
		"n", // no companions
		"2", // save only
	), "\n") + "\n"
	stdin = byteReader{strings.NewReader(answers)}
	start := config.Default()
	start.Mods.Repos = []string{repo.URL}
	start.Session.User = "gamer"
	f := detect.Facts{OS: "linux", Pretty: "Test Linux", Tailscale: "100.64.0.9", Immutable: true,
		ModelServers: []detect.ModelServer{{Kind: "almanac", Container: "almanac", Unit: "almanac-gateway.service"}}}
	c, result, err := Run(start, f, false, func(config.Config) string { return "review" })
	if err != nil {
		t.Fatal(err)
	}
	if result != SaveOnly {
		t.Fatalf("result %v", result)
	}
	checks := map[string]bool{
		"topology host (no Incus)":              c.Topology == config.TopologyHost,
		"user kept on an empty answer":          c.Session.User == "gamer",
		"resolution 1920x1080@120":              c.Game.Width == 1920 && c.Game.Height == 1080 && c.Game.FPS == 120,
		"bitrate 80 Mbit/s":                     c.Stream.MaxBitrateKbps == 80000,
		"listen on Tailscale":                   c.Stream.ListenAddress == "100.64.0.9",
		"sound not stream-only":                 !c.Audio.StreamOnly,
		"almanac reservation":                   c.Share.Mode == config.ShareReserve && c.Share.Container == "almanac" && c.Share.TokenFile != "",
		"mods: Almanac, testing, no companions": len(c.Mods.Install) == 1 && c.Mods.Install[0] == "Almanac" && c.Mods.Testing && !c.Mods.Companions,
	}
	for what, ok := range checks {
		if !ok {
			t.Errorf("%s: %+v", what, c)
		}
	}
}
