package detect

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLinuxGPUs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads a Linux sysfs layout (built from symlinks)")
	}
	root := t.TempDir()
	mk := func(card, pci, vendor, driver string, render string) {
		dev := filepath.Join(root, "devices", pci)
		_ = os.MkdirAll(filepath.Join(dev, "drm", render), 0o755)
		_ = os.WriteFile(filepath.Join(dev, "vendor"), []byte(vendor+"\n"), 0o644)
		_ = os.MkdirAll(filepath.Join(root, "drivers", driver), 0o755)
		_ = os.Symlink(filepath.Join(root, "drivers", driver), filepath.Join(dev, "driver"))
		_ = os.MkdirAll(filepath.Join(root, "drm", card), 0o755)
		_ = os.Symlink(dev, filepath.Join(root, "drm", card, "device"))
	}
	mk("card0", "0000:00:02.0", "0x8086", "i915", "renderD128")
	mk("card1", "0000:01:00.0", "0x10de", "nvidia", "renderD129")
	_ = os.MkdirAll(filepath.Join(root, "drm", "card1-DP-1"), 0o755)
	gpus := linuxGPUs(filepath.Join(root, "drm"))
	if len(gpus) != 2 {
		t.Fatalf("%d GPUs: %+v", len(gpus), gpus)
	}
	if gpus[0].Vendor != "nvidia" || gpus[0].RenderNode != "/dev/dri/renderD129" || !gpus[0].Discrete || gpus[0].Driver != "nvidia" {
		t.Fatalf("the discrete card comes first: %+v", gpus[0])
	}
	if gpus[1].Discrete {
		t.Fatal("bus 00 is the iGPU")
	}
}

func TestModelServers(t *testing.T) {
	proc := t.TempDir()
	add := func(pid, cmdline, cgroup string) {
		d := filepath.Join(proc, pid)
		_ = os.MkdirAll(d, 0o755)
		_ = os.WriteFile(filepath.Join(d, "cmdline"), []byte(cmdline), 0o644)
		_ = os.WriteFile(filepath.Join(d, "cgroup"), []byte(cgroup), 0o644)
	}
	add("1", "/home/almanac/.local/share/almanac/venv/bin/python3\x00/home/almanac/.local/share/almanac/venv/bin/almanac\x00gateway\x00", "0::/lxc.payload.almanac/user.slice/x\n")
	add("2", "/usr/bin/ollama\x00serve\x00", "0::/lxc.payload.almanac/system.slice/ollama.service\n")
	add("3", "/usr/bin/ollama\x00serve\x00", "0::/system.slice/ollama.service\n")
	got := modelServers(proc)
	want := []ModelServer{{"almanac", "almanac", "almanac-gateway.service"}, {"ollama", "", "ollama.service"}, {"ollama", "almanac", "ollama.service"}}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestDevice(t *testing.T) {
	for vendor, product := range map[string]string{"Valve": "Jupiter", "GPD": "G1619-04"} {
		if device(vendor, product) == "" {
			t.Errorf("%s %s is a handheld", vendor, product)
		}
	}
	if device("Dell Inc.", "Alienware Aurora R13") != "" {
		t.Error("a desktop is no handheld")
	}
}
