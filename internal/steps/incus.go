package steps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Spaceghost/ffxiv-stream/internal/config"
	"github.com/Spaceghost/ffxiv-stream/internal/plan"
	"github.com/Spaceghost/ffxiv-stream/internal/sys"
)

// Port is one port a backend listens on.
type Port struct {
	Name  string
	Proto string // tcp, udp
	Num   int
}

// Ports lists what clients must reach, per backend.
func Ports(c config.Config) []Port {
	switch c.Backend {
	case config.BackendSelkies:
		return []Port{{"web", "tcp", c.Selkies.Port}}
	default: // Sunshine and Wolf use Moonlight's layout around the base port
		b := c.Stream.PortBase
		return []Port{
			{"https", "tcp", b - 5}, {"http", "tcp", b}, {"webui", "tcp", b + 1}, {"rtsp", "tcp", b + 21},
			{"video", "udp", b + 9}, {"control", "udp", b + 10}, {"audio", "udp", b + 11}, {"mic", "udp", b + 13},
		}
	}
}

func (b *builder) incus() ([]plan.Step, error) {
	c := b.c
	host := plan.Local{}
	ct := plan.Incus{Container: c.Incus.Container}
	name := c.Incus.Container
	var s []plan.Step

	attachGPU := func() error {
		if hasDevice(name, "gpu") {
			return nil
		}
		pci := c.Incus.GPU
		if pci == "" {
			gpu, ok := b.f.PrimaryGPU()
			if !ok {
				return fmt.Errorf("no GPU found to pass through")
			}
			pci = gpu.PCI
		}
		if b.nvidia() {
			if _, err := sys.Output("incus", "config", "set", name, "nvidia.runtime=true", "nvidia.driver.capabilities=all"); err != nil {
				return err
			}
		}
		_, err := sys.Output("incus", "config", "device", "add", name, "gpu", "gpu", "gputype=physical", "pci="+pci)
		return err
	}

	// One streaming container per host: the stream's virtual input devices all
	// appear in the host's /dev/input under the same names, so a second
	// container's rule would hand (or reveal) one client's input to the other.
	s = append(s, step(host, "Check that no other container streams from this host",
		"Two streaming containers would see each other's streamed keyboard and mouse.",
		[]string{"ls /etc/udev/rules.d/70-ffxiv-stream-*.rules"},
		func() (bool, error) { return otherStreamer(name) == "", nil },
		func() error {
			return fmt.Errorf("container %q already streams from this host (%s); one streaming container per host keeps each client's input private. Remove it first, or set incus.container = %q",
				otherStreamer(name), "/etc/udev/rules.d/70-ffxiv-stream-"+otherStreamer(name)+".rules", otherStreamer(name))
		}))

	create := []string{"incus", "init", c.Incus.Image, name}
	if c.Incus.CPU != "" {
		create = append(create, "-c", "limits.cpu="+c.Incus.CPU)
	}
	if c.Incus.Memory != "" {
		create = append(create, "-c", "limits.memory="+c.Incus.Memory)
	}
	s = append(s, step(host, "Create the Incus container "+name+" ("+c.Incus.Image+")", "",
		[]string{sys.Quote(create), "(GPU attached before the first start)", "incus start " + name},
		func() (bool, error) { return containerExists(name), nil },
		func() error {
			if _, err := sys.Output(create...); err != nil {
				return err
			}
			// The GPU goes on before the first start: nvidia.runtime only applies
			// at start, and systemd in a container that has only just booted
			// ignores the shutdown signal a restart sends (a minute's wait).
			if err := attachGPU(); err != nil {
				return err
			}
			if _, err := sys.Output("incus", "start", name); err != nil {
				return err
			}
			// Wait for its network before anything downloads inside it.
			_, err := ct.Run("sh", "-c", "for i in $(seq 120); do getent hosts github.com >/dev/null && exit 0; sleep 1; done; exit 1")
			return err
		}))

	s = append(s, step(host, "Pass the GPU through", "The game renders and the stream encodes on it.",
		[]string{"incus config device add " + name + " gpu gpu gputype=physical pci=<the streaming GPU>", "incus config set " + name + " nvidia.runtime=true nvidia.driver.capabilities=all  (NVIDIA)"},
		func() (bool, error) { return hasDevice(name, "gpu"), nil },
		func() error {
			if err := attachGPU(); err != nil {
				return err
			}
			// An existing container: nvidia.runtime applies at start.
			_, err := sys.Output("incus", "restart", "--timeout", "90", name)
			return err
		}))

	s = append(s, step(host, "Give the container uinput, uhid and the host's /dev/input",
		"The streaming server creates the client's keyboard, mouse and pads through uinput/uhid; their event nodes appear in the host's /dev/input.",
		[]string{"incus config device add " + name + " uinput unix-char source=/dev/uinput gid=<input> mode=0660",
			"incus config device add " + name + " uhid unix-char source=/dev/uhid gid=<input> mode=0660",
			"incus config device add " + name + " host-input disk source=/dev/input path=/dev/input"},
		func() (bool, error) {
			return hasDevice(name, "uinput") && hasDevice(name, "uhid") && hasDevice(name, "host-input"), nil
		},
		func() error {
			gid, err := ct.Run("sh", "-c", "getent group input >/dev/null || groupadd -r input; getent group input | cut -d: -f3")
			if err != nil {
				return err
			}
			for dev, src := range map[string]string{"uinput": "/dev/uinput", "uhid": "/dev/uhid"} {
				if !hasDevice(name, dev) {
					if _, err := sys.Output("incus", "config", "device", "add", name, dev, "unix-char", "source="+src, "path="+src, "gid="+gid, "mode=0660"); err != nil {
						return err
					}
				}
			}
			if !hasDevice(name, "host-input") {
				_, err = sys.Output("incus", "config", "device", "add", name, "host-input", "disk", "source=/dev/input", "path=/dev/input")
			}
			return err
		}))

	s = append(s, step(host, "Pin the container's address", "The stream's port forwards need an address that does not change.",
		[]string{"incus config device override " + name + " eth0 ipv4.address=<its current address>"},
		func() (bool, error) {
			out, _ := sys.Output("incus", "config", "device", "get", name, "eth0", "ipv4.address")
			return out != "", nil
		},
		func() error {
			ip, err := containerIP(name)
			if err != nil {
				return err
			}
			if _, err := sys.Output("incus", "config", "device", "override", name, "eth0", "ipv4.address="+ip); err != nil {
				if _, err2 := sys.Output("incus", "config", "device", "set", name, "eth0", "ipv4.address="+ip); err2 != nil {
					return err
				}
			}
			return nil
		}))

	s = append(s, step(host, "Forward the stream's ports to the container",
		"NAT forwards in the kernel, not incus's userspace proxy: the proxy dropped UDP video packets under load, which looked like flicker.",
		portShows(c, name),
		func() (bool, error) {
			for _, p := range Ports(c) {
				if !hasDevice(name, "stream-"+p.Name) {
					return false, nil
				}
			}
			return true, nil
		},
		func() error {
			ip, err := containerIP(name)
			if err != nil {
				return err
			}
			listen := listenAddress(c, b.f.Tailscale)
			for _, p := range Ports(c) {
				dev := "stream-" + p.Name
				if hasDevice(name, dev) {
					continue
				}
				if _, err := sys.Output("incus", "config", "device", "add", name, dev, "proxy", "nat=true",
					fmt.Sprintf("listen=%s:%s:%d", p.Proto, listen, p.Num), fmt.Sprintf("connect=%s:%s:%d", p.Proto, ip, p.Num)); err != nil {
					return err
				}
			}
			return nil
		}))

	s = append(s, b.installSelf(host, ct)...)
	s = append(s, b.session(ct, true)...)
	udev := step(host, "Install the host udev rule for the stream's input devices",
		"Hands just the stream's virtual devices to the container's user; every other input device on this host stays out of its reach.",
		[]string{"write /etc/udev/rules.d/70-ffxiv-stream-" + name + ".rules", "udevadm control --reload"},
		func() (bool, error) {
			want, err := b.udevRule(ct)
			if err != nil {
				return false, err
			}
			old, err := os.ReadFile("/etc/udev/rules.d/70-ffxiv-stream-" + name + ".rules")
			return err == nil && bytes.Equal(old, want), nil
		},
		func() error {
			want, err := b.udevRule(ct)
			if err != nil {
				return err
			}
			if err := host.WriteFile("/etc/udev/rules.d/70-ffxiv-stream-"+name+".rules", want, 0o644, ""); err != nil {
				return err
			}
			_, err = sys.Output("udevadm", "control", "--reload")
			return err
		})
	s = append(s, udev)
	s = append(s, b.hostServices(host)...)
	u := c.Session.User
	return append(s, b.modSteps(ct, "/home/"+u+"/.xlcore", u+":"+u)...), nil
}

func portShows(c config.Config, name string) []string {
	var out []string
	for _, p := range Ports(c) {
		out = append(out, fmt.Sprintf("incus config device add %s stream-%s proxy nat=true listen=%s:<listen>:%d connect=%s:<container>:%d", name, p.Name, p.Proto, p.Num, p.Proto, p.Num))
	}
	return out
}

// listenAddress is where clients connect: the configured address, else this
// host's Tailscale address, else its primary address.
func listenAddress(c config.Config, tailscale string) string {
	if c.Stream.ListenAddress != "" {
		return c.Stream.ListenAddress
	}
	if tailscale != "" {
		return tailscale
	}
	out, err := sys.Output("sh", "-c", "ip -4 route get 1.1.1.1 | sed -n 's/.* src \\([0-9.]*\\).*/\\1/p'")
	if err == nil && out != "" {
		return out
	}
	return "0.0.0.0"
}

// udevRule renders the host rule with the container user's and input group's
// ids as the host sees them, through the container's id map.
func (b *builder) udevRule(ct plan.Incus) ([]byte, error) {
	name := b.c.Incus.Container
	raw, err := sys.Output("incus", "config", "get", name, "volatile.idmap.current")
	if err != nil {
		return nil, err
	}
	uidBase, gidBase, err := idmapBases(raw)
	if err != nil {
		return nil, err
	}
	uid, err := ct.Run("id", "-u", b.c.Session.User)
	if err != nil {
		return nil, fmt.Errorf("the container has no user %s yet", b.c.Session.User)
	}
	gid, err := ct.Run("sh", "-c", "getent group input | cut -d: -f3")
	if err != nil {
		return nil, err
	}
	u, _ := strconv.Atoi(uid)
	g, _ := strconv.Atoi(gid)
	v := b.view(true)
	v.HostUID, v.HostGID = uidBase+u, gidBase+g
	return render("udev.rules", v), nil
}

// idmapBases reads the host ids that container id 0 maps to.
func idmapBases(raw string) (uid, gid int, err error) {
	var maps []struct {
		Isuid, Isgid bool
		Hostid, Nsid int
	}
	if err := json.Unmarshal([]byte(raw), &maps); err != nil {
		return 0, 0, fmt.Errorf("volatile.idmap.current: %w", err)
	}
	uid, gid = -1, -1
	for _, m := range maps {
		if m.Nsid != 0 {
			continue
		}
		if m.Isuid && uid < 0 {
			uid = m.Hostid
		}
		if m.Isgid && gid < 0 {
			gid = m.Hostid
		}
	}
	if uid < 0 || gid < 0 {
		return 0, 0, fmt.Errorf("volatile.idmap.current has no mapping for id 0")
	}
	return uid, gid, nil
}

func containerExists(name string) bool {
	out, err := sys.Output("incus", "list", "^"+name+"$", "-c", "n", "-f", "csv")
	return err == nil && strings.TrimSpace(out) == name
}

func hasDevice(name, dev string) bool {
	out, err := sys.Output("incus", "config", "device", "list", name)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == dev {
			return true
		}
	}
	return false
}

func containerIP(name string) (string, error) {
	out, err := sys.Output("incus", "list", "^"+name+"$", "-c", "4", "-f", "csv")
	if err != nil {
		return "", err
	}
	for _, field := range strings.Fields(out) {
		if strings.Count(field, ".") == 3 {
			return field, nil
		}
	}
	return "", fmt.Errorf("%s has no IPv4 address yet", name)
}

// installSelf puts this binary on the host and into the container (the
// container's services run its input-bridge, prepare-gpu and render-node).
func (b *builder) installSelf(host plan.Local, ct plan.Incus) []plan.Step {
	same := func(t plan.Target) func() (bool, error) {
		return func() (bool, error) {
			self, err := selfBinary()
			if err != nil {
				return false, err
			}
			old, err := t.ReadFile("/usr/local/bin/ffxiv-stream")
			return err == nil && bytes.Equal(bytes.TrimRight(old, "\n"), bytes.TrimRight(self, "\n")), nil
		}
	}
	install := func(t plan.Target) func() error {
		return func() error {
			self, err := selfBinary()
			if err != nil {
				return err
			}
			return t.WriteFile("/usr/local/bin/ffxiv-stream", self, 0o755, "")
		}
	}
	return []plan.Step{
		step(host, "Install ffxiv-stream on this host", "Its services run it.", []string{"install -m 755 ffxiv-stream /usr/local/bin/"},
			sameOrPackaged(host, same(host)), install(host)),
		step(ct, "Install ffxiv-stream in the container", "The input bridge and GPU preparation run it.", []string{"incus file push ffxiv-stream " + ct.Container + "/usr/local/bin/"},
			same(ct), install(ct)),
	}
}

// sameOrPackaged: a packaged install (in /usr/bin) is left alone on the host.
func sameOrPackaged(t plan.Target, same func() (bool, error)) func() (bool, error) {
	return func() (bool, error) {
		if exe, err := os.Executable(); err == nil && strings.HasPrefix(exe, "/usr/bin/") {
			return true, nil
		}
		return same()
	}
}

// hostServices: the config file the services read, starting the container
// once its listen address exists, and GPU sharing.
func (b *builder) hostServices(host plan.Local) []plan.Step {
	c := b.c
	v := b.view(false)
	data, _ := c.Encode()
	s := []plan.Step{plan.File(host, "/etc/ffxiv-stream/config.toml", data, 0o600, "", "Write /etc/ffxiv-stream/config.toml", "The host services read it.")}
	switch {
	case b.f.Distro == "nixos":
		// NixOS declares services in its configuration; units written into /etc
		// would not survive a rebuild. The flake's module does what these steps do.
		return append(s, step(host, "Declare the services in your NixOS configuration",
			"On NixOS: imports = [ ffxiv-stream.nixosModules.default ]; services.ffxiv-stream.enable = true;",
			[]string{"services.ffxiv-stream = { enable = true; configFile = /etc/ffxiv-stream/config.toml; };"},
			succeeds(host, "systemctl", "cat", "ffxiv-stream-gpu-share.service"),
			func() error {
				return fmt.Errorf("add the ffxiv-stream NixOS module (see the README's NixOS section), rebuild, then run apply again")
			}))
	case b.f.Init == "openrc":
		return append(s, b.openrcServices(host)...)
	}
	var enable []string
	if c.Topology == config.TopologyIncus && c.Incus.Autostart {
		s = append(s, plan.File(host, "/etc/systemd/system/ffxiv-stream-container.service", render("container.service", v), 0o644, "",
			"Write the container start service", "Incus refuses to start a container whose forwards listen on an address that does not exist yet (Tailscale comes up late in boot)."))
		enable = append(enable, "ffxiv-stream-container.service")
	}
	if c.Share.Mode != config.ShareNone {
		s = append(s, plan.File(host, "/etc/systemd/system/ffxiv-stream-gpu-share.service", render("gpu-share.service", v), 0o644, "",
			"Write the GPU sharing service", "Gives the game the GPU's memory while it runs ("+c.Share.Mode+")."))
		enable = append(enable, "ffxiv-stream-gpu-share.service")
	}
	if len(enable) > 0 {
		s = append(s, step(host, "Enable the host services", "", []string{"systemctl enable --now " + strings.Join(enable, " ")},
			succeeds(host, append([]string{"systemctl", "is-enabled", "-q"}, enable...)...),
			func() error {
				if _, err := sys.Output("systemctl", "daemon-reload"); err != nil {
					return err
				}
				_, err := sys.Output(append([]string{"systemctl", "enable", "--now"}, enable...)...)
				return err
			}))
	}
	return s
}

// openrcServices: the same services for OpenRC hosts (Alpine, Gentoo, Artix).
func (b *builder) openrcServices(host plan.Local) []plan.Step {
	c := b.c
	script := func(name, desc, args, stop string) []byte {
		return []byte(fmt.Sprintf(`#!/sbin/openrc-run
# ffxiv-stream: %s (generated by ffxiv-stream)
description="%s"
command=/usr/local/bin/ffxiv-stream
command_args="%s"
command_background=true
pidfile="/run/${RC_SVCNAME}.pid"
output_log="/var/log/${RC_SVCNAME}.log"
error_log="/var/log/${RC_SVCNAME}.log"
%s
depend() {
	need net
	after incusd
}
`, desc, desc, args, stop))
	}
	var s []plan.Step
	var names []string
	if c.Topology == config.TopologyIncus && c.Incus.Autostart {
		s = append(s, plan.File(host, "/etc/init.d/ffxiv-stream-container",
			[]byte(strings.Replace(string(script("container", "start the "+c.Incus.Container+" container once its stream address exists", "start-container", "")),
				"command_background=true\n", "", 1)), 0o755, "", "Write the container start service (OpenRC)", ""))
		names = append(names, "ffxiv-stream-container")
	}
	if c.Share.Mode != config.ShareNone {
		s = append(s, plan.File(host, "/etc/init.d/ffxiv-stream-gpu-share",
			script("gpu-share", "give the game the GPU's memory while it runs", "gpu-share",
				"stop_post() {\n\t/usr/local/bin/ffxiv-stream gpu-share --stop\n}\n"), 0o755, "", "Write the GPU sharing service (OpenRC)", ""))
		names = append(names, "ffxiv-stream-gpu-share")
	}
	for _, n := range names {
		s = append(s, step(host, "Enable "+n+" (OpenRC)", "", []string{"rc-update add " + n + " default", "rc-service " + n + " start"},
			succeeds(host, "sh", "-c", "rc-update show default | grep -q '"+n+"'"),
			func() error {
				_, err := sys.Output("sh", "-c", "rc-update add "+n+" default && rc-service "+n+" start")
				return err
			}))
	}
	return s
}

// otherStreamer names another container ffxiv-stream set up on this host, if any.
func otherStreamer(name string) string {
	rules, _ := filepath.Glob("/etc/udev/rules.d/70-ffxiv-stream-*.rules")
	for _, r := range rules {
		other := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(r), "70-ffxiv-stream-"), ".rules")
		if other != name {
			return other
		}
	}
	return ""
}
