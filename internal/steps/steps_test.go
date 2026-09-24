package steps

import (
	"testing"

	"github.com/Spaceghost/ffxiv-stream/internal/config"
)

func TestIdmapBases(t *testing.T) {
	raw := `[{"Isuid":true,"Isgid":false,"Hostid":1000000,"Nsid":0,"Maprange":1000000000},{"Isuid":false,"Isgid":true,"Hostid":1000000,"Nsid":0,"Maprange":1000000000}]`
	uid, gid, err := idmapBases(raw)
	if err != nil || uid != 1000000 || gid != 1000000 {
		t.Fatalf("%d %d %v", uid, gid, err)
	}
	if _, _, err := idmapBases(`[]`); err == nil {
		t.Fatal("an empty map is an error")
	}
}

func TestPorts(t *testing.T) {
	c := config.Default()
	byName := map[string]Port{}
	for _, p := range Ports(c) {
		byName[p.Name] = p
	}
	// Moonlight's fixed layout around 47989.
	for name, want := range map[string]int{"https": 47984, "http": 47989, "webui": 47990, "rtsp": 48010, "video": 47998, "control": 47999, "audio": 48000, "mic": 48002} {
		if byName[name].Num != want {
			t.Errorf("%s = %d, want %d", name, byName[name].Num, want)
		}
	}
	c.Backend = config.BackendSelkies
	if p := Ports(c); len(p) != 1 || p[0].Num != 8080 || p[0].Proto != "tcp" {
		t.Fatalf("selkies: %v", p)
	}
}

func TestWithoutAdapter(t *testing.T) {
	if withoutAdapter("a = 1\nadapter_name = /dev/dri/renderD129\n") != withoutAdapter("a = 1\n") {
		t.Fatal("the adapter line is the stream loop's, not a config change")
	}
}
