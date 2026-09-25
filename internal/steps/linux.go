package steps

import (
	"fmt"
	"os"
	"strings"

	"github.com/Spaceghost/ffxiv-stream/internal/assets"
	"github.com/Spaceghost/ffxiv-stream/internal/config"
	"github.com/Spaceghost/ffxiv-stream/internal/plan"
	"github.com/Spaceghost/ffxiv-stream/internal/releases"
)

// Packages the headless session needs, per package manager. Xwayland runs the
// game (Wine is an X11 client here); gnome-keyring and libsecret let
// XIVLauncher keep the login; gcc builds the NVIDIA capture shim.
var sessionPackages = map[string][]string{
	"dnf":    {"sway", "xorg-x11-server-Xwayland", "pipewire", "wireplumber", "pipewire-pulseaudio", "gnome-keyring", "libsecret", "curl", "tar", "gzip", "gcc"},
	"apt":    {"sway", "xwayland", "pipewire", "wireplumber", "pipewire-pulse", "gnome-keyring", "libsecret-tools", "curl", "tar", "gzip", "gcc", "libc6-dev"},
	"pacman": {"sway", "xorg-xwayland", "pipewire", "wireplumber", "pipewire-pulse", "gnome-keyring", "libsecret", "curl", "tar", "gzip", "gcc"},
	"zypper": {"sway", "xwayland", "pipewire", "wireplumber", "pipewire-pulseaudio", "gnome-keyring", "libsecret-tools", "curl", "tar", "gzip", "gcc"},
}

// NVIDIA's EGL external platforms, per package manager.
var nvidiaEGL = map[string][]string{
	"dnf":    {"egl-gbm", "egl-wayland"},
	"apt":    {"libnvidia-egl-gbm1", "libnvidia-egl-wayland1"},
	"pacman": {"egl-gbm", "egl-wayland"},
	"zypper": {"libnvidia-egl-gbm1", "libnvidia-egl-wayland1"},
}

func installCmd(pm string, pkgs ...string) []string {
	switch pm {
	case "dnf":
		return append([]string{"dnf", "-y", "install"}, pkgs...)
	case "apt":
		return []string{"sh", "-c", "DEBIAN_FRONTEND=noninteractive apt-get update -q && DEBIAN_FRONTEND=noninteractive apt-get install -y -q " + strings.Join(pkgs, " ")}
	case "pacman":
		return append([]string{"pacman", "-Sy", "--noconfirm", "--needed"}, pkgs...)
	case "zypper":
		return append([]string{"zypper", "--non-interactive", "install"}, pkgs...)
	}
	return nil
}

// packageManager finds the target's package manager when the step runs.
func packageManager(t plan.Target) (string, error) {
	for _, pm := range []string{"dnf", "apt-get", "pacman", "zypper"} {
		if _, err := t.Run("sh", "-c", "command -v "+pm); err == nil {
			return strings.TrimSuffix(pm, "-get"), nil
		}
	}
	return "", fmt.Errorf("%s: no supported package manager (dnf, apt, pacman, zypper)", t.Name())
}

// session returns the steps that make t run the headless streamed session
// for user (inside an Incus container when inContainer).
func (b *builder) session(t plan.Target, inContainer bool) []plan.Step {
	c := b.c
	v := b.view(inContainer)
	u := c.Session.User
	home := "/home/" + u
	owner := u + ":" + u
	lib := "/usr/local/lib/ffxiv-stream"
	var s []plan.Step

	s = append(s, step(t, "Install the session packages (sway, Xwayland, PipeWire, keyring)",
		"The headless compositor the stream captures, sound, and a Secret Service for the launcher's password.",
		[]string{"<package manager> install sway xwayland pipewire wireplumber gnome-keyring libsecret gcc ..."},
		succeeds(t, "sh", "-c", "command -v sway && command -v Xwayland && command -v wireplumber && command -v gnome-keyring-daemon && command -v secret-tool"),
		func() error {
			pm, err := packageManager(t)
			if err != nil {
				return err
			}
			_, err = t.Run(installCmd(pm, sessionPackages[pm]...)...)
			return err
		}))

	if b.nvidia() {
		s = append(s, step(t, "Install NVIDIA's EGL platform libraries (egl-gbm, egl-wayland)",
			"The driver passed through from the host brings EGL but not its GBM platform: without it the streaming server's capture cannot import frames (\"Couldn't initialize EGL display\") and falls back to CPU encoding.",
			[]string{"<package manager> install egl-gbm egl-wayland"},
			succeeds(t, "sh", "-c", "ls /usr/share/egl/egl_external_platform.d/*nvidia_gbm*.json"),
			func() error {
				pm, err := packageManager(t)
				if err != nil {
					return err
				}
				_, err = t.Run(installCmd(pm, nvidiaEGL[pm]...)...)
				return err
			}))
	}
	s = append(s, b.backendInstall(t)...)

	s = append(s, step(t, "Install XIVLauncher.Core in /opt/xivlauncher",
		"The launcher (and Dalamud) for the game. Always the newest release of goatcorp/XIVLauncher.Core.",
		[]string{"curl -L <newest XIVLauncher.Core.tar.gz> | tar -xz -C /opt/xivlauncher"},
		exists(t, "/opt/xivlauncher/XIVLauncher.Core"),
		func() error {
			a, err := releases.Find("goatcorp/XIVLauncher.Core", `^XIVLauncher\.Core\.tar\.gz$`, false)
			if err != nil {
				return err
			}
			_, err = t.Run("sh", "-c", `set -e; mkdir -p /opt/xivlauncher; curl -fsSL "$1" | tar -xz -C /opt/xivlauncher`, "-", a.URL)
			return err
		}))

	s = append(s, step(t, "Create the session user "+u,
		"The game runs as an ordinary user in the video, render and input groups (GPU nodes and the stream's virtual input devices).",
		[]string{"useradd -m " + u, "usermod -aG video,render,input " + u, "loginctl enable-linger " + u},
		succeeds(t, "sh", "-c", fmt.Sprintf(`id -nG %s | tr ' ' '\n' | grep -qx input && id -nG %s | tr ' ' '\n' | grep -qx video && test -e /var/lib/systemd/linger/%s`, u, u, u)),
		func() error {
			_, err := t.Run("sh", "-c", fmt.Sprintf(`set -e
id %[1]s >/dev/null 2>&1 || useradd -m %[1]s
for g in video render input; do getent group $g >/dev/null || groupadd -r $g; done
usermod -aG video,render,input %[1]s
loginctl enable-linger %[1]s`, u))
			return err
		}))

	s = append(s,
		plan.File(t, lib+"/session", render("session.sh.tmpl", v), 0o755, "", "Write the session script", "Starts the headless sway the stream captures."),
		plan.File(t, lib+"/launcher", render("launcher.sh.tmpl", v), 0o755, "", "Write the launcher loop", "Keeps XIVLauncher on screen: the stream is the only way in."),
		plan.File(t, lib+"/stream", render("stream.sh.tmpl", v), 0o755, "", "Write the streaming server loop", "Starts "+c.Backend+" from inside sway so it captures the right output."),
		plan.File(t, "/etc/ffxiv-stream/sway.conf", render("sway.conf.tmpl", v), 0o644, "", "Write the session's sway config", "Fullscreen everything; flat mouse acceleration for streamed mice."),
	)
	if c.Audio.StreamOnly {
		s = append(s, plan.File(t, "/etc/wireplumber/wireplumber.conf.d/90-ffxiv-stream-audio.conf", render("wireplumber.conf", v), 0o644, "",
			"Keep sound in the stream", "WirePlumber gets no ALSA or Bluetooth outputs, so the game can only be heard through the stream."))
	}

	s = append(s, step(t, "Make sure "+u+" owns its config directories",
		"Files written as root into the home must not leave ~/.config or ~/.local root's: the session could not save anything.",
		[]string{"chown -R " + owner + " " + home + "/.config " + home + "/.local  (only what is not " + u + "'s)"},
		succeeds(t, "sh", "-c", fmt.Sprintf(`test -z "$(find %[1]s/.config %[1]s/.local -maxdepth 4 ! -user %[2]s 2>/dev/null | head -n1)"`, home, u)),
		func() error {
			_, err := t.Run("sh", "-c", fmt.Sprintf(`for d in %[1]s/.config %[1]s/.local; do [ -e "$d" ] && find "$d" ! -user %[2]s -exec chown %[3]s {} +; done; true`, home, u, owner))
			return err
		}))

	switch c.Backend {
	case config.BackendSunshine:
		conf := sunshineConf(v)
		s = append(s,
			sunshineConfStep(t, home+"/.config/sunshine/sunshine.conf", conf, owner),
			plan.File(t, home+"/.config/sunshine/apps.json", []byte(appsJSON), 0o644, owner, "Write Sunshine's app list",
				"One app, the session itself: the launcher is already on screen."),
			step(t, "Set Sunshine's web UI login ("+c.Stream.WebUser+")", "Pairing clients happens through Sunshine's web UI or `ffxiv-stream pair`.",
				[]string{"sunshine --creds " + c.Stream.WebUser + " <password>  (as " + u + ")"},
				// Done when set by an earlier apply, or when Sunshine already has a
				// login (an existing setup): never replace a password someone chose.
				succeeds(t, "sh", "-c", `test -e `+home+`/.config/ffxiv-stream/sunshine-creds-set || grep -q '"username"' `+home+`/.config/sunshine/sunshine_state.json`),
				func() error {
					pw, err := secret("sunshine-web-password", c.Stream.WebPassword)
					if err != nil {
						return err
					}
					// The password goes over stdin, never on a command line (or into an error message).
					_, err = t.RunInput([]byte(pw), "su", "-", u, "-c",
						fmt.Sprintf(`sunshine --creds %s "$(cat)" >/dev/null && mkdir -p ~/.config/ffxiv-stream && touch ~/.config/ffxiv-stream/sunshine-creds-set`, shq(c.Stream.WebUser)))
					return err
				}))
	case config.BackendSelkies:
		s = append(s, step(t, "Write Selkies' login", "Selkies refuses to start with basic auth and no password.",
			[]string{"write " + home + "/.config/ffxiv-stream/selkies.env (mode 600)"},
			succeeds(t, "grep", "-q", "^SELKIES_BASIC_AUTH_PASSWORD=", home+"/.config/ffxiv-stream/selkies.env"),
			func() error {
				pw, err := secret("selkies-password", c.Selkies.Password)
				if err != nil {
					return err
				}
				env := fmt.Sprintf("SELKIES_BASIC_AUTH_PASSWORD=%s\n", pw)
				return t.WriteFile(home+"/.config/ffxiv-stream/selkies.env", []byte(env), 0o600, owner)
			}))
	}

	if b.nvidia() && c.Backend == config.BackendSunshine {
		s = append(s, step(t, "Build the dmabuf-wait capture shim (NVIDIA)",
			"NVIDIA's EGL ignores the implicit fence on captured frames; without the shim the stream flickers black under load.",
			[]string{"gcc -O2 -shared -fPIC dmabuf-wait.c -o <libdir>/libdmabuf-wait.so -ldl", "chmod 4755 <libdir>/libdmabuf-wait.so"},
			succeeds(t, "sh", "-c", `d=$(dirname "$(gcc -print-file-name=libc.so.6)"); test -u "$d/libdmabuf-wait.so"`),
			func() error {
				if err := t.WriteFile(lib+"/dmabuf-wait.c", assets.Raw("dmabuf-wait.c"), 0o644, ""); err != nil {
					return err
				}
				_, err := t.Run("sh", "-c", `set -e; d=$(cd "$(dirname "$(gcc -print-file-name=libc.so.6)")" && pwd -P)
gcc -O2 -Wall -shared -fPIC /usr/local/lib/ffxiv-stream/dmabuf-wait.c -o "$d/libdmabuf-wait.so" -ldl
chmod 4755 "$d/libdmabuf-wait.so"`)
				return err
			}))
	}

	// The login keyring: a plaintext one (empty password), which gnome-keyring
	// reads without a prompt. `gnome-keyring-daemon --unlock ""` does not create it.
	s = append(s, step(t, "Create the login keyring (for the launcher's password)",
		"XIVLauncher keeps the account password through libsecret; there is no login screen to unlock a keyring at.",
		[]string{"write " + home + "/.local/share/keyrings/login.keyring (empty password, mode 600)"},
		exists(t, home+"/.local/share/keyrings/login.keyring"),
		func() error {
			if err := t.WriteFile(home+"/.local/share/keyrings/login.keyring",
				[]byte("[keyring]\ndisplay-name=login\nctime=0\nmtime=0\nlock-on-idle=false\nlock-after=false\n"), 0o600, owner); err != nil {
				return err
			}
			if t.Exists(home + "/.local/share/keyrings/default") {
				return nil
			}
			return t.WriteFile(home+"/.local/share/keyrings/default", []byte("login"), 0o644, owner)
		}))

	units := home + "/.config/systemd/user"
	s = append(s,
		plan.File(t, units+"/ffxiv-stream-keyring.service", render("keyring.service", v), 0o644, owner, "Write the keyring user service", ""),
		plan.File(t, units+"/ffxiv-stream-session.service", render("session.service", v), 0o644, owner, "Write the session user service", ""),
	)
	if inContainer {
		s = append(s,
			plan.File(t, "/etc/systemd/system/ffxiv-stream-prepare.service", render("prepare.service", v), 0o644, "", "Write the GPU preparation service",
				"Incus recreates the GPU's nodes and driver libraries at every start; they need fixing before the session."),
			plan.File(t, "/etc/systemd/system/ffxiv-stream-input-bridge.service", render("input-bridge.service", v), 0o644, "", "Write the input bridge service",
				"Announces the stream's virtual keyboard, mouse and pads to the container's udev."),
		)
	}
	s = append(s, b.retireOldSetup(t, u)...)
	s = append(s, step(t, "Enable and start the services", "",
		[]string{"systemctl enable --now ffxiv-stream-prepare ffxiv-stream-input-bridge", "systemctl --user -M " + u + "@ enable --now ffxiv-stream-keyring ffxiv-stream-session"},
		succeeds(t, "sh", "-c", fmt.Sprintf("systemctl --user -M %s@ is-active -q ffxiv-stream-session", u)),
		func() error {
			if inContainer {
				if _, err := t.Run("systemctl", "daemon-reload"); err != nil {
					return err
				}
				if _, err := t.Run("systemctl", "enable", "--now", "ffxiv-stream-prepare.service", "ffxiv-stream-input-bridge.service"); err != nil {
					return err
				}
			}
			if _, err := t.Run("systemctl", "--user", "-M", u+"@", "daemon-reload"); err != nil {
				return err
			}
			_, err := t.Run("systemctl", "--user", "-M", u+"@", "enable", "--now", "ffxiv-stream-keyring.service", "ffxiv-stream-session.service")
			return err
		}))
	restart := step(t, "Restart the session to use what changed",
		"Never while the game runs: that would end it. Then it is left for the next start.",
		[]string{"systemctl --user -M " + u + "@ restart ffxiv-stream-session"}, nil,
		func() error {
			if out, _ := t.Run("sh", "-c", `pgrep -f 'ffxiv_dx11\.exe' || true`); strings.TrimSpace(out) != "" {
				fmt.Print("(the game is running: restart skipped; the changes apply when the session next starts) ")
				return nil
			}
			if inContainer {
				if _, err := t.Run("systemctl", "restart", "ffxiv-stream-input-bridge.service"); err != nil {
					return err
				}
			}
			_, err := t.Run("systemctl", "--user", "-M", u+"@", "restart", "ffxiv-stream-session.service")
			return err
		})
	restart.IfChanged = true
	return append(s, restart)
}

// backendInstall installs the streaming server itself.
func (b *builder) backendInstall(t plan.Target) []plan.Step {
	switch b.c.Backend {
	case config.BackendSunshine:
		return []plan.Step{step(t, "Install Sunshine",
			"Fedora: LizardByte's Copr repository. Others: the newest release package from LizardByte/Sunshine.",
			[]string{"dnf copr enable -y lizardbyte/stable && dnf install -y Sunshine", "(apt/pacman: the release .deb / .pkg.tar.zst)"},
			succeeds(t, "sh", "-c", "command -v sunshine"),
			func() error {
				pm, err := packageManager(t)
				if err != nil {
					return err
				}
				switch pm {
				case "dnf":
					_, err = t.Run("sh", "-c", "dnf -y install dnf-plugins-core >/dev/null 2>&1 || true; dnf -y copr enable lizardbyte/stable && dnf -y install Sunshine")
					return err
				case "apt":
					return installRelease(t, "LizardByte/Sunshine", `ubuntu-24\.04-amd64\.deb$`, "apt-get install -y ./pkg.deb", "pkg.deb")
				case "pacman":
					return installRelease(t, "LizardByte/Sunshine", `\.pkg\.tar\.zst$`, "pacman -U --noconfirm ./pkg.pkg.tar.zst", "pkg.pkg.tar.zst")
				}
				return fmt.Errorf("Sunshine: no install route for %s", pm)
			})}
	case config.BackendSelkies:
		return []plan.Step{step(t, "Install Selkies",
			"The newest release package from selkies-project/selkies for this distribution.",
			[]string{"install the newest selkies release (.rpm/.deb/.pkg.tar.zst)"},
			succeeds(t, "sh", "-c", "command -v selkies"),
			func() error {
				pm, err := packageManager(t)
				if err != nil {
					return err
				}
				switch pm {
				case "dnf":
					return installRelease(t, "selkies-project/selkies", `-fc-x86_64\.rpm$`, "dnf -y install ./pkg.rpm", "pkg.rpm")
				case "apt":
					return installRelease(t, "selkies-project/selkies", `ubuntu24\.04.*amd64\.deb$`, "apt-get install -y ./pkg.deb", "pkg.deb")
				case "pacman":
					return installRelease(t, "selkies-project/selkies", `x86_64\.pkg\.tar\.zst$`, "pacman -U --noconfirm ./pkg.pkg.tar.zst", "pkg.pkg.tar.zst")
				}
				return fmt.Errorf("Selkies: no install route for %s", pm)
			})}
	}
	return nil
}

// installRelease downloads a release asset into a temporary directory on t
// and runs install there.
func installRelease(t plan.Target, repo, pattern, install, file string) error {
	a, err := releases.Find(repo, pattern, false)
	if err != nil {
		return err
	}
	_, err = t.Run("sh", "-c", `set -e; d=$(mktemp -d); cd "$d"; curl -fsSL -o "$2" "$1"; sh -c "$3"; cd /; rm -rf "$d"`, "-", a.URL, file, install)
	return err
}

const appsJSON = `{
  "env": {},
  "apps": [
    {
      "name": "Final Fantasy XIV",
      "image-path": "desktop.png"
    }
  ]
}
`

func sunshineConf(v view) []byte { return render("sunshine.conf.tmpl", v) }

// sunshineConfStep writes sunshine.conf, ignoring the adapter_name line the
// stream loop maintains at every start.
func sunshineConfStep(t plan.Target, path string, content []byte, owner string) plan.Step {
	st := plan.File(t, path, content, 0o644, owner, "Write Sunshine's config", "Capture, encoder, bitrate cap, codecs and pad type.")
	st.Check = func() (bool, error) {
		old, err := t.ReadFile(path)
		if err != nil {
			return false, nil
		}
		return withoutAdapter(string(old)) == withoutAdapter(string(content)), nil
	}
	return st
}

func withoutAdapter(s string) string {
	var keep []string
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if !strings.HasPrefix(line, "adapter_name") {
			keep = append(keep, line)
		}
	}
	return strings.Join(keep, "\n")
}

// retireOldSetup turns off the hand-made units this project grew out of, if a
// machine still has them, so two sessions never run at once.
func (b *builder) retireOldSetup(t plan.Target, u string) []plan.Step {
	return []plan.Step{step(t, "Turn off units from a pre-ffxiv-stream setup, if any",
		"ffxiv-session, ffxiv-input-bridge, ffxiv-prepare and ffxiv-keyring are superseded by the ffxiv-stream-* units.",
		[]string{"systemctl disable --now ffxiv-input-bridge ffxiv-prepare", "systemctl --user -M " + u + "@ disable --now ffxiv-session ffxiv-keyring"},
		succeeds(t, "sh", "-c", fmt.Sprintf(`! systemctl is-enabled -q ffxiv-input-bridge.service 2>/dev/null && ! systemctl --user -M %s@ is-enabled -q ffxiv-session.service 2>/dev/null`, u)),
		func() error {
			_, err := t.Run("sh", "-c", fmt.Sprintf(`systemctl disable --now ffxiv-input-bridge.service ffxiv-prepare.service 2>/dev/null
systemctl --user -M %s@ disable --now ffxiv-session.service ffxiv-keyring.service 2>/dev/null; true`, u))
			return err
		})}
}

// shq quotes a word for sh.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// selfBinary is this executable, to install on the host or push into a container.
func selfBinary() ([]byte, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return os.ReadFile(exe)
}
