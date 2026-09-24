package releases

import (
	"regexp"
	"testing"
)

func TestPick(t *testing.T) {
	rels := []release{
		{TagName: "v2-test", Prerelease: true, Assets: []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		}{{"tool.fc43.x86_64.rpm", "https://x/pre.rpm"}}},
		{TagName: "v1", Assets: []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		}{{"tool.fc43.x86_64.rpm", "https://x/v1.rpm"}, {"tool.apk", "https://x/v1.apk"}}},
	}
	re := regexp.MustCompile(`\.fc43\.x86_64\.rpm$`)
	if a, _ := pick(rels, re, false, "r"); a.URL != "https://x/v1.rpm" {
		t.Fatalf("stable: %v", a)
	}
	if a, _ := pick(rels, re, true, "r"); a.Tag != "v2-test" {
		t.Fatalf("prerelease allowed: %v", a)
	}
	if _, err := pick(rels, regexp.MustCompile(`\.exe$`), true, "r"); err == nil {
		t.Fatal("no match must be an error")
	}
}
