// Package mods installs Dalamud plugins for the game without the in-game
// installer: it adds the plugin repositories to Dalamud's configuration and
// lays each chosen plugin out exactly as Dalamud's own installer does
// (installedPlugins/<InternalName>/<version>/ with the repository manifest
// plus the fields Dalamud adds on install). Dalamud loads them at the next
// game start.
//
// Dalamud rewrites dalamudConfig.json while the game runs, so steps that touch
// it refuse to run then (see GameRunning in the caller).
package mods

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

// Plugin is one entry of a Dalamud repository (plugins.json).
type Plugin struct {
	InternalName           string   `json:"InternalName"`
	Name                   string   `json:"Name"`
	Author                 string   `json:"Author"`
	Punchline              string   `json:"Punchline"`
	Description            string   `json:"Description"`
	AssemblyVersion        string   `json:"AssemblyVersion"`
	TestingAssemblyVersion string   `json:"TestingAssemblyVersion"`
	DownloadLinkInstall    string   `json:"DownloadLinkInstall"`
	DownloadLinkTesting    string   `json:"DownloadLinkTesting"`
	DalamudApiLevel        int      `json:"DalamudApiLevel"`
	Tags                   []string `json:"Tags"`
	RepoURL                string   `json:"-"` // the repository it came from
	raw                    map[string]any
}

// Version and download link for the chosen channel.
func (p Plugin) Pick(testing bool) (version, url string, isTesting bool) {
	if testing && p.TestingAssemblyVersion != "" && p.DownloadLinkTesting != "" {
		return p.TestingAssemblyVersion, p.DownloadLinkTesting, true
	}
	return p.AssemblyVersion, p.DownloadLinkInstall, false
}

// Needs names plugins this one depends on, by what its listing says. Dalamud
// has no dependency field; these are the known ones in the family.
func (p Plugin) Needs() []string {
	switch p.InternalName {
	case "XivDesktop":
		return []string{"GhosttyDalamud"}
	case "Almanac":
		return []string{"XivMcp"}
	}
	return nil
}

var client = &http.Client{Timeout: 2 * time.Minute}

// Fetch reads a repository's plugin list.
func Fetch(url string) ([]Plugin, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return Parse(body, url)
}

func Parse(body []byte, url string) ([]Plugin, error) {
	var raws []map[string]any
	if err := json.Unmarshal(body, &raws); err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	var plugins []Plugin
	for _, r := range raws {
		data, _ := json.Marshal(r)
		var p Plugin
		if err := json.Unmarshal(data, &p); err != nil || p.InternalName == "" {
			continue
		}
		p.RepoURL, p.raw = url, r
		plugins = append(plugins, p)
	}
	return plugins, nil
}

// Resolve expands a selection with the plugins it needs, in install order.
func Resolve(available []Plugin, wanted []string) ([]Plugin, error) {
	byName := map[string]Plugin{}
	for _, p := range available {
		byName[p.InternalName] = p
	}
	var order []Plugin
	seen := map[string]bool{}
	var add func(string) error
	add = func(name string) error {
		if seen[name] {
			return nil
		}
		p, ok := byName[name]
		if !ok {
			return fmt.Errorf("plugin %q is in no configured repository", name)
		}
		seen[name] = true
		for _, dep := range p.Needs() {
			if err := add(dep); err != nil {
				return err
			}
		}
		order = append(order, p)
		return nil
	}
	for _, name := range wanted {
		if err := add(name); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// Download fetches a plugin's zip.
func Download(url string) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// Manifest is what Dalamud writes next to an installed plugin: the repository
// entry plus its own bookkeeping.
func Manifest(p Plugin, version string, testing bool) ([]byte, error) {
	m := map[string]any{}
	for k, v := range p.raw {
		m[k] = v
	}
	m["AssemblyVersion"] = version
	m["Disabled"] = false
	m["Testing"] = testing
	m["ScheduledForDeletion"] = false
	m["InstalledFromUrl"] = p.RepoURL
	m["WorkingPluginId"] = uuid()
	m["IsThirdParty"] = true
	// Dalamud keeps these empty in an installed manifest.
	for _, k := range []string{"DownloadLinkInstall", "DownloadLinkUpdate", "DownloadLinkTesting"} {
		m[k] = nil
	}
	return json.MarshalIndent(m, "", "  ")
}

// Tree is an installed plugin directory: relative path -> content.
type Tree map[string][]byte

// Unpack reads a plugin zip into a Tree, replacing the zip's manifest with
// the installed one.
func Unpack(zipData []byte, p Plugin, version string, testing bool) (Tree, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.InternalName, err)
	}
	tree := Tree{}
	for _, f := range zr.File {
		name := path.Clean(strings.ReplaceAll(f.Name, `\`, "/"))
		if f.FileInfo().IsDir() || strings.HasPrefix(name, "../") || path.IsAbs(name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		tree[name] = data
	}
	if _, ok := tree[p.InternalName+".dll"]; !ok {
		return nil, fmt.Errorf("%s: the zip has no %s.dll", p.InternalName, p.InternalName)
	}
	manifest, err := Manifest(p, version, testing)
	if err != nil {
		return nil, err
	}
	tree[p.InternalName+".json"] = manifest
	return tree, nil
}

// Tar packs a tree (for writing into a container in one go).
func (t Tree) Tar() []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	names := make([]string, 0, len(t))
	for n := range t {
		names = append(names, n)
	}
	sort.Strings(names)
	now := time.Now()
	for _, n := range names {
		_ = tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(t[n])), ModTime: now, Typeflag: tar.TypeReg})
		_, _ = tw.Write(t[n])
	}
	_ = tw.Close()
	return buf.Bytes()
}

// ConfigureDalamud adds the repositories and testing opt-ins to a
// dalamudConfig.json (nil or empty: a new one), keeping everything else.
func ConfigureDalamud(existing []byte, repos []string, testing []string) ([]byte, error) {
	cfg := map[string]any{}
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := json.Unmarshal(existing, &cfg); err != nil {
			return nil, fmt.Errorf("dalamudConfig.json: %w", err)
		}
	}
	if _, ok := cfg["$type"]; !ok {
		cfg["$type"] = "Dalamud.Configuration.Internal.DalamudConfiguration, Dalamud"
	}
	repoList := list(cfg, "ThirdRepoList", "System.Collections.Generic.List`1[[Dalamud.Configuration.ThirdPartyRepoSettings, Dalamud]], System.Private.CoreLib")
	for _, url := range repos {
		found := false
		for _, r := range repoList.values() {
			if m, ok := r.(map[string]any); ok && m["Url"] == url {
				m["IsEnabled"], found = true, true
			}
		}
		if !found {
			repoList.add(map[string]any{"$type": "Dalamud.Configuration.ThirdPartyRepoSettings, Dalamud", "Url": url, "IsEnabled": true})
		}
	}
	cfg["ThirdRepoSpeedbumpDismissed"] = true
	if len(testing) > 0 {
		cfg["DoPluginTest"] = true
		optIns := list(cfg, "PluginTestingOptIns", "System.Collections.Generic.List`1[[Dalamud.Configuration.Internal.PluginTestingOptIn, Dalamud]], System.Private.CoreLib")
		for _, name := range testing {
			found := false
			for _, o := range optIns.values() {
				if m, ok := o.(map[string]any); ok && m["InternalName"] == name {
					found = true
				}
			}
			if !found {
				optIns.add(map[string]any{"$type": "Dalamud.Configuration.Internal.PluginTestingOptIn, Dalamud", "InternalName": name, "Branch": "testing-live"})
			}
		}
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// jsonList is a Newtonsoft-typed list ({"$type": ..., "$values": [...]}).
type jsonList struct{ m map[string]any }

func list(cfg map[string]any, key, typ string) jsonList {
	m, ok := cfg[key].(map[string]any)
	if !ok {
		m = map[string]any{"$type": typ, "$values": []any{}}
		cfg[key] = m
	}
	if _, ok := m["$values"].([]any); !ok {
		m["$values"] = []any{}
	}
	return jsonList{m}
}

func (l jsonList) values() []any { return l.m["$values"].([]any) }
func (l jsonList) add(v any)     { l.m["$values"] = append(l.values(), v) }

func uuid() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Dir is where a launcher keeps Dalamud's files, per OS and launcher.
func Dir(goos, home string) string {
	switch goos {
	case "windows":
		return path.Join(os.Getenv("APPDATA"), "XIVLauncher")
	case "darwin":
		return path.Join(home, "Library", "Application Support", "XIV on Mac")
	default:
		return path.Join(home, ".xlcore")
	}
}
