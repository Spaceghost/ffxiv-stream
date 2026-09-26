package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegacy(t *testing.T) {
	for in, want := range map[string]string{
		"/etc/xivstream/config.toml":            "/etc/ffxiv-stream/config.toml",
		"/var/lib/xivstream":                    "/var/lib/ffxiv-stream",
		"/home/p/.config/xivstream/config.toml": "/home/p/.config/ffxiv-stream/config.toml",
		"/etc/other/config.toml":                "",
	} {
		in, want = filepath.FromSlash(in), filepath.FromSlash(want)
		if got := Legacy(in); got != want {
			t.Errorf("Legacy(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadFallsBackToTheOldName(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("XIVSTREAM_CONFIG", "")
	if os.Geteuid() == 0 {
		t.Skip("as root the path is /etc/xivstream")
	}
	path := Path()
	old := Legacy(path)
	if old == "" {
		t.Skip("no per-user config path on this system")
	}
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("[stream]\nname = \"from the old name\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Stream.Name != "from the old name" {
		t.Fatalf("stream name %q: the old config was not read", c.Stream.Name)
	}
}
