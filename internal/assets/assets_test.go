package assets

import (
	"strings"
	"testing"

	"github.com/Spaceghost/ffxiv-stream/internal/config"
)

type view struct {
	config.Config
	InContainer, NVIDIA, Headless bool
	Encoder, LauncherCommand      string
	HostUID, HostGID              int
	InputMarks                    []string
}

func TestEveryTemplateRendersForEveryBackend(t *testing.T) {
	for _, backend := range []string{config.BackendSunshine, config.BackendSelkies, config.BackendWolf} {
		for _, nvidia := range []bool{true, false} {
			c := config.Default()
			c.Backend = backend
			v := view{Config: c, InContainer: true, NVIDIA: nvidia, Headless: true, Encoder: "nvenc",
				LauncherCommand: "/opt/xivlauncher/XIVLauncher.Core", HostUID: 1001000, HostGID: 1000104, InputMarks: []string{"libvirtualhid"}}
			for _, name := range []string{"session.sh.tmpl", "launcher.sh.tmpl", "stream.sh.tmpl", "sway.conf.tmpl", "sunshine.conf.tmpl",
				"session.service", "keyring.service", "prepare.service", "input-bridge.service", "container.service", "gpu-share.service", "udev.rules", "wireplumber.conf"} {
				out, err := Render(name, v)
				if err != nil {
					t.Fatalf("%s/%v/%s: %v", backend, nvidia, name, err)
				}
				if strings.Contains(string(out), "<no value>") {
					t.Errorf("%s/%s: unresolved value:\n%s", backend, name, out)
				}
			}
		}
	}
}

func TestStreamScriptPerBackend(t *testing.T) {
	c := config.Default()
	v := view{Config: c, NVIDIA: true}
	if s := string(MustRender("stream.sh.tmpl", v)); !strings.Contains(s, "LD_PRELOAD=libdmabuf-wait.so sunshine") {
		t.Errorf("sunshine on NVIDIA needs the shim:\n%s", s)
	}
	v.NVIDIA = false
	if s := string(MustRender("stream.sh.tmpl", v)); strings.Contains(s, "LD_PRELOAD") {
		t.Error("no shim off NVIDIA")
	}
	v.Backend = config.BackendSelkies
	if s := string(MustRender("stream.sh.tmpl", v)); !strings.Contains(s, "--wayland=true --wayland-host-display") || !strings.Contains(s, "--port=8080") || strings.Contains(s, "--basic-auth-password") {
		t.Errorf("selkies:\n%s", s)
	}
	rule := string(MustRender("udev.rules", view{Config: c, HostUID: 1001000, HostGID: 1000104, InputMarks: []string{"libvirtualhid"}}))
	if !strings.Contains(rule, `ATTRS{name}=="*libvirtualhid*"`) || !strings.Contains(rule, "chown 1001000:1000104 $devnode") {
		t.Errorf("udev rule:\n%s", rule)
	}
	if conf := string(MustRender("sunshine.conf.tmpl", view{Config: c, Headless: true, Encoder: "nvenc"})); !strings.Contains(conf, "capture = wlr") || !strings.Contains(conf, "hevc_mode = 1") {
		t.Errorf("sunshine.conf:\n%s", conf)
	}
}
