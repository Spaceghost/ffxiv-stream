package steps

import (
	"fmt"
	"strings"

	"github.com/Spaceghost/xivstream-dalamud/internal/config"
	"github.com/Spaceghost/xivstream-dalamud/internal/plan"
	"github.com/Spaceghost/xivstream-dalamud/internal/sys"
)

// linuxHost: the game and the server run on this Linux machine directly.
// Headless: a dedicated session for Session.User (a spare PC, a server).
// Otherwise: Sunshine streams the desktop you are logged in to.
func (b *builder) linuxHost() ([]plan.Step, error) {
	c := b.c
	host := plan.Local{}
	u := c.Session.User
	var s []plan.Step
	s = append(s, b.installSelf(host, plan.Incus{})[0])
	if c.Session.Headless {
		s = append(s, b.session(host, false)...)
	} else {
		s = append(s, b.backendInstall(host)...)
		s = append(s, step(host, "Start Sunshine with your desktop session", "Sunshine captures the desktop you are logged in to.",
			[]string{"systemctl --user -M " + u + "@ enable --now sunshine"},
			succeeds(host, "systemctl", "--user", "-M", u+"@", "is-enabled", "-q", "sunshine"),
			func() error {
				_, err := sys.Output("systemctl", "--user", "-M", u+"@", "enable", "--now", "sunshine")
				return err
			}))
	}
	s = append(s, b.hostServices(host)...)
	return append(s, b.modSteps(host, "/home/"+u+"/.xlcore", u+":"+u)...), nil
}

// flatpakHost: read-only systems (Bazzite, SteamOS, Kinoite, ...): Flatpaks,
// and Sunshine streaming the desktop or Game Mode you are in.
func (b *builder) flatpakHost() ([]plan.Step, error) {
	c := b.c
	host := plan.Local{}
	u := c.Session.User
	var s []plan.Step
	if c.Backend != config.BackendSunshine {
		return nil, fmt.Errorf("on %s use backend = sunshine (Selkies needs packages this system does not layer)", b.f.Pretty)
	}
	s = append(s, step(host, "Install Flathub's XIVLauncher", "dev.goats.xivlauncher: the launcher and Dalamud, sandboxed.",
		[]string{"flatpak install -y --noninteractive flathub dev.goats.xivlauncher"},
		succeeds(host, "flatpak", "info", "dev.goats.xivlauncher"),
		func() error {
			_, err := sys.Output("flatpak", "install", "-y", "--noninteractive", "flathub", "dev.goats.xivlauncher")
			return err
		}))
	if b.f.Sunshine != "" { // Bazzite ships it
		s = append(s, step(host, "Start the system's Sunshine with your session", "Bazzite ships Sunshine; this enables it for "+u+".",
			[]string{"systemctl --user -M " + u + "@ enable --now sunshine"},
			succeeds(host, "systemctl", "--user", "-M", u+"@", "is-enabled", "-q", "sunshine"),
			func() error {
				_, err := sys.Output("systemctl", "--user", "-M", u+"@", "enable", "--now", "sunshine")
				return err
			}))
	} else {
		s = append(s, step(host, "Install Flathub's Sunshine", "dev.lizardbyte.app.Sunshine, plus its input rules and autostart (its own installer script).",
			[]string{"flatpak install -y --noninteractive flathub dev.lizardbyte.app.Sunshine", "flatpak run --command=additional-install.sh dev.lizardbyte.app.Sunshine"},
			succeeds(host, "flatpak", "info", "dev.lizardbyte.app.Sunshine"),
			func() error {
				if _, err := sys.Output("flatpak", "install", "-y", "--noninteractive", "flathub", "dev.lizardbyte.app.Sunshine"); err != nil {
					return err
				}
				_, err := sys.Output("su", "-", u, "-c", "flatpak run --command=additional-install.sh dev.lizardbyte.app.Sunshine")
				return err
			}))
	}
	s = append(s, b.hostServices(host)...)
	return append(s, b.modSteps(host, "/home/"+u+"/.var/app/dev.goats.xivlauncher/data/xlcore", u+":"+u)...), nil
}

// wolf: Wolf in a rootful Podman container (a Quadlet unit), running the game
// in an XIVLauncher app container per client. Experimental: see docs/backends.md.
func (b *builder) wolf() ([]plan.Step, error) {
	c := b.c
	host := plan.Local{}
	if !b.f.Podman && !b.f.Docker {
		return nil, fmt.Errorf("wolf needs podman or docker")
	}
	var s []plan.Step
	s = append(s,
		step(host, "Install Wolf's udev rules", "Virtual pads get their own seat, so a streamed controller cannot drive this machine's desktop.",
			[]string{"curl -fsSL https://raw.githubusercontent.com/games-on-whales/wolf/stable/85-wolf.rules -o /etc/udev/rules.d/85-wolf.rules", "udevadm control --reload-rules && udevadm trigger"},
			exists(host, "/etc/udev/rules.d/85-wolf.rules"),
			func() error {
				_, err := sys.Output("sh", "-c", "curl -fsSL https://raw.githubusercontent.com/games-on-whales/wolf/stable/85-wolf.rules -o /etc/udev/rules.d/85-wolf.rules && udevadm control --reload-rules && udevadm trigger")
				return err
			}),
		plan.File(host, "/etc/modules-load.d/xivstream-uhid.conf", []byte("uhid\n"), 0o644, "", "Load uhid at boot", "Otherwise it loads on demand and virtual pads fail with permission errors."),
		plan.File(host, "/etc/wolf/cfg/config.toml", wolfConfig(c), 0o644, "", "Write Wolf's app list", "One app: XIVLauncher in xivstream's app image."),
		plan.File(host, "/etc/containers/systemd/wolf.container", wolfQuadlet(c, primaryNode(b), b.nvidia()), 0o644, "", "Write Wolf's Quadlet unit", "Rootful: Wolf starts app containers itself, which rootless Podman cannot (device cgroup rules)."),
		step(host, "Enable the Podman socket", "Wolf starts the game's app containers through it.",
			[]string{"systemctl enable --now podman.socket"},
			succeeds(host, "systemctl", "is-active", "-q", "podman.socket"),
			func() error { _, err := sys.Output("systemctl", "enable", "--now", "podman.socket"); return err }),
		step(host, "Start Wolf", "", []string{"systemctl daemon-reload && systemctl start wolf"},
			succeeds(host, "systemctl", "is-active", "-q", "wolf"),
			func() error {
				_, err := sys.Output("sh", "-c", "systemctl daemon-reload && systemctl start wolf")
				return err
			}),
	)
	return append(s, b.hostServices(host)...), nil
}

// WolfImage is the app image: XIVLauncher on games-on-whales' base-app (see images/xivlauncher).
const WolfImage = "ghcr.io/spaceghost/xivstream-xivlauncher:latest"

func wolfConfig(c config.Config) []byte {
	return []byte(fmt.Sprintf(`# xivstream: Wolf's apps. Wolf reads this at start and rewrites it only
# when a client pairs; stop Wolf before editing by hand. (Generated by xivstream.)
[[profiles]]
id = 'moonlight-profile-id'
  [[profiles.apps]]
  title = 'Final Fantasy XIV'
  start_virtual_compositor = true
    [profiles.apps.runner]
    type = 'docker'
    name = 'WolfFFXIV'
    image = '%s'
    env = ['RUN_SWAY=1', 'GOW_REQUIRED_DEVICES=/dev/input/* /dev/dri/* /dev/nvidia*', 'DXVK_FRAME_RATE=%d']
    mounts = []
    devices = []
    ports = []
    base_create_json = '''{"HostConfig":{"IpcMode":"host","Privileged":false,"CapAdd":["NET_RAW","MKNOD","NET_ADMIN"],"DeviceCgroupRules":["c 13:* rmw","c 244:* rmw"]}}'''
`, WolfImage, c.Game.FPS))
}

// wolfQuadlet follows Wolf's documented rootful Podman setup: host network,
// the Podman socket as Docker's (Wolf starts the app containers), /dev and
// /run/udev for the virtual devices, and the API socket for `xivstream pair`.
func wolfQuadlet(c config.Config, renderNode string, nvidia bool) []byte {
	var env strings.Builder
	if renderNode != "" {
		env.WriteString("Environment=WOLF_RENDER_NODE=" + renderNode + "\n")
	}
	if b := c.Stream.PortBase; b != 47989 {
		fmt.Fprintf(&env, "Environment=WOLF_HTTP_PORT=%d\nEnvironment=WOLF_HTTPS_PORT=%d\nEnvironment=WOLF_CONTROL_PORT=%d\nEnvironment=WOLF_RTSP_SETUP_PORT=%d\n", b, b-5, b+10, b+21)
	}
	gpu := ""
	if nvidia {
		// CDI (nvidia-container-toolkit >= 1.16): Wolf documents the driver-volume
		// route as steadier; CDI is what a Podman host normally has.
		gpu = "AddDevice=nvidia.com/gpu=all\nEnvironment=NVIDIA_DRIVER_CAPABILITIES=all\nEnvironment=NVIDIA_VISIBLE_DEVICES=all\n"
	}
	return []byte(`# xivstream: Wolf, rootful. (Generated by xivstream.)
[Unit]
Description=Wolf game streaming server (xivstream)
After=network-online.target podman.socket
Wants=network-online.target podman.socket

[Container]
ContainerName=wolf
Image=ghcr.io/games-on-whales/wolf:stable
Network=host
SecurityLabelDisable=true
PodmanArgs=--ipc=host --device-cgroup-rule "c 13:* rmw"
Volume=/etc/wolf:/etc/wolf:rw
Volume=/run/podman/podman.sock:/var/run/docker.sock:rw
Volume=/dev:/dev:rw
Volume=/run/udev:/run/udev:rw
Volume=/var/run/wolf:/var/run/wolf:rw
AddDevice=/dev/dri
AddDevice=/dev/uinput
AddDevice=/dev/uhid
Environment=WOLF_SOCKET_PATH=/var/run/wolf/wolf.sock
` + env.String() + gpu + `
[Service]
ExecStartPre=/usr/bin/mkdir -p /var/run/wolf
Restart=always

[Install]
WantedBy=multi-user.target
`)
}

func primaryNode(b *builder) string {
	gpu, _ := b.f.PrimaryGPU()
	return gpu.RenderNode
}
