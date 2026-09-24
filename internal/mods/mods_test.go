package mods

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"testing"
)

const repo = `[
 {"InternalName":"GhosttyDalamud","Name":"Ghostty","AssemblyVersion":"0.3.0.2","TestingAssemblyVersion":"0.3.1.7",
  "DownloadLinkInstall":"https://x/g.zip","DownloadLinkTesting":"https://x/g-t.zip","DalamudApiLevel":15},
 {"InternalName":"XivDesktop","Name":"XivDesktop","AssemblyVersion":"1.0.1.0","DownloadLinkInstall":"https://x/d.zip"},
 {"InternalName":"XivMcp","AssemblyVersion":"0.1.0.4","DownloadLinkInstall":"https://x/m.zip"},
 {"InternalName":"Almanac","AssemblyVersion":"0.2.0.2","DownloadLinkInstall":"https://x/a.zip"},
 {"Name":"no internal name"}
]`

func TestParseAndResolve(t *testing.T) {
	plugins, err := Parse([]byte(repo), "https://repo/plugins.json")
	if err != nil || len(plugins) != 4 {
		t.Fatalf("Parse: %v, %d plugins", err, len(plugins))
	}
	order, err := Resolve(plugins, []string{"XivDesktop", "Almanac"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range order {
		names = append(names, p.InternalName)
	}
	if got := join(names); got != "GhosttyDalamud XivDesktop XivMcp Almanac" {
		t.Fatalf("order %q: dependencies must come first", got)
	}
	if _, err := Resolve(plugins, []string{"Nope"}); err == nil {
		t.Fatal("an unknown plugin must be an error")
	}
	v, url, testing := plugins[0].Pick(true)
	if v != "0.3.1.7" || url != "https://x/g-t.zip" || !testing {
		t.Fatalf("testing pick: %s %s %v", v, url, testing)
	}
	if v, _, testing := plugins[1].Pick(true); v != "1.0.1.0" || testing {
		t.Fatal("no testing build: the stable one")
	}
}

func TestUnpackWritesInstalledManifest(t *testing.T) {
	plugins, _ := Parse([]byte(repo), "https://repo/plugins.json")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range []string{"GhosttyDalamud.dll", "GhosttyDalamud.json", "lua/init.lua"} {
		w, _ := zw.Create(n)
		_, _ = w.Write([]byte("x"))
	}
	_ = zw.Close()
	tree, err := Unpack(buf.Bytes(), plugins[0], "0.3.1.7", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tree["lua/init.lua"]; !ok {
		t.Fatal("subdirectories are kept")
	}
	var m map[string]any
	_ = json.Unmarshal(tree["GhosttyDalamud.json"], &m)
	if m["InstalledFromUrl"] != "https://repo/plugins.json" || m["Testing"] != true || m["AssemblyVersion"] != "0.3.1.7" || m["WorkingPluginId"] == "" {
		t.Fatalf("manifest: %v", m)
	}
	if m["DownloadLinkInstall"] != nil {
		t.Fatal("download links are cleared in an installed manifest")
	}
}

func TestConfigureDalamudKeepsTheRest(t *testing.T) {
	existing := []byte(`{"$type":"Dalamud.Configuration.Internal.DalamudConfiguration, Dalamud","LanguageOverride":"en",
	 "ThirdRepoList":{"$type":"L","$values":[{"$type":"R","Url":"https://repo/plugins.json","IsEnabled":false}]}}`)
	out, err := ConfigureDalamud(existing, []string{"https://repo/plugins.json", "https://other/plugins.json"}, []string{"GhosttyDalamud"})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	_ = json.Unmarshal(out, &cfg)
	if cfg["LanguageOverride"] != "en" || cfg["$type"] == nil {
		t.Fatal("other settings must survive")
	}
	repos := cfg["ThirdRepoList"].(map[string]any)["$values"].([]any)
	if len(repos) != 2 || repos[0].(map[string]any)["IsEnabled"] != true {
		t.Fatalf("repos: %v", repos)
	}
	if cfg["DoPluginTest"] != true || len(cfg["PluginTestingOptIns"].(map[string]any)["$values"].([]any)) != 1 {
		t.Fatal("testing opt-in")
	}
	again, _ := ConfigureDalamud(out, []string{"https://repo/plugins.json"}, []string{"GhosttyDalamud"})
	_ = json.Unmarshal(again, &cfg)
	if n := len(cfg["ThirdRepoList"].(map[string]any)["$values"].([]any)); n != 2 {
		t.Fatalf("re-running must not duplicate repos (%d)", n)
	}
	if fresh, err := ConfigureDalamud(nil, []string{"https://repo/plugins.json"}, nil); err != nil || !bytes.Contains(fresh, []byte("ThirdRepoList")) {
		t.Fatal("a missing config gets a new one")
	}
}

func join(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += " "
		}
		out += v
	}
	return out
}
