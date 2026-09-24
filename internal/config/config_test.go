package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	c := Default()
	c.Mods.Install = []string{"GhosttyDalamud"}
	c.Stream.ListenAddress = "100.64.0.1"
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stream.ListenAddress != "100.64.0.1" || len(got.Mods.Install) != 1 || got.Session.User != "player" {
		t.Fatalf("round trip: %+v", got)
	}
}

func TestPartialFileKeepsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	_ = os.WriteFile(path, []byte("backend = \"selkies\"\n[game]\nfps = 30\n"), 0o600)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Backend != "selkies" || c.Game.FPS != 30 || c.Game.Width != 1920 || c.Selkies.Port != 8080 {
		t.Fatalf("partial: %+v", c)
	}
}

func TestRejects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.toml")
	for body, want := range map[string]string{
		"backend = \"vnc\"\n":                        "backend",
		"topology = \"cloud\"\n":                     "topology",
		"[gpu_share]\nmode = \"stop-unit\"\n":        "unit",
		"typo = 1\n":                                 "unknown keys",
		"[stream]\ncodecs = \"mpeg2\"\n":             "codecs",
		"backend = \"wolf\"\ntopology = \"incus\"\n": "wolf",
	} {
		_ = os.WriteFile(path, []byte(body), 0o600)
		if _, err := Load(path); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want an error about %s", body, err, want)
		}
	}
}
