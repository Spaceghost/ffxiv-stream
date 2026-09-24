// Package releases finds download links on GitHub releases at apply time, so
// ffxiv-stream always installs the current Sunshine, Selkies, XIVLauncher and
// ghostty-agent instead of versions frozen into it.
package releases

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"time"
)

type Asset struct {
	Tag, Name, URL string
}

type release struct {
	TagName    string `json:"tag_name"`
	Prerelease bool   `json:"prerelease"`
	Draft      bool   `json:"draft"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

var client = &http.Client{Timeout: time.Minute}

// Find returns the first asset matching pattern in the newest release of
// owner/repo; prereleases count only when pre is true.
func Find(repo, pattern string, pre bool) (Asset, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Asset{}, err
	}
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/"+repo+"/releases?per_page=20", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" { // optional: lifts the anonymous rate limit
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := client.Do(req)
	if err != nil {
		return Asset{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Asset{}, fmt.Errorf("github %s releases: %s", repo, resp.Status)
	}
	var rels []release
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		return Asset{}, err
	}
	return pick(rels, re, pre, repo)
}

func pick(rels []release, re *regexp.Regexp, pre bool, repo string) (Asset, error) {
	for _, r := range rels {
		if r.Draft || (r.Prerelease && !pre) {
			continue
		}
		for _, a := range r.Assets {
			if re.MatchString(a.Name) {
				return Asset{Tag: r.TagName, Name: a.Name, URL: a.URL}, nil
			}
		}
	}
	return Asset{}, fmt.Errorf("no release of %s has an asset matching %s", repo, re)
}
