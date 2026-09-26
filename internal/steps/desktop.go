package steps

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Spaceghost/xivstream-dalamud/internal/config"
	"github.com/Spaceghost/xivstream-dalamud/internal/plan"
	"github.com/Spaceghost/xivstream-dalamud/internal/sys"
)

// windows: Sunshine (its installer registers its own service) and XIVLauncher
// through winget; xivstream's GPU sharing as a Windows service.
func (b *builder) windows() ([]plan.Step, error) {
	c := b.c
	host := plan.Local{}
	if b.f.Packages != "winget" {
		return nil, fmt.Errorf("winget is needed (App Installer from the Microsoft Store)")
	}
	winget := func(id, title, why string, installed func() (bool, error)) plan.Step {
		argv := []string{"winget", "install", "--id", id, "-e", "--silent", "--accept-package-agreements", "--accept-source-agreements"}
		return step(host, title, why, []string{sys.Quote(argv)}, installed, func() error { _, err := sys.Output(argv...); return err })
	}
	pf := os.Getenv("ProgramFiles")
	sunshineDir := filepath.Join(pf, "Sunshine")
	local := os.Getenv("LOCALAPPDATA")
	v := b.view(false)
	v.Headless = false
	s := []plan.Step{
		winget("LizardByte.Sunshine", "Install Sunshine", "The streaming server; its installer adds the SunshineService.",
			exists(host, filepath.Join(sunshineDir, "sunshine.exe"))),
		winget("goatcorp.XIVLauncher", "Install XIVLauncher", "The launcher and Dalamud.",
			exists(host, filepath.Join(local, "XIVLauncher", "XIVLauncher.exe"))),
		plan.File(host, filepath.Join(sunshineDir, "config", "sunshine.conf"), sunshineConf(v), 0o644, "", "Write Sunshine's config", "Encoder, bitrate cap, codecs and pad type."),
		plan.File(host, filepath.Join(sunshineDir, "config", "apps.json"), []byte(fmt.Sprintf(`{
  "env": {},
  "apps": [
    {
      "name": "Final Fantasy XIV",
      "cmd": %q,
      "image-path": "desktop.png"
    }
  ]
}
`, filepath.Join(local, "XIVLauncher", "XIVLauncher.exe"))), 0o644, "", "Write Sunshine's app list", "Starting the app opens XIVLauncher."),
		step(host, "Set Sunshine's web UI login ("+c.Stream.WebUser+")", "", []string{"sunshine.exe --creds " + c.Stream.WebUser + " <password>"},
			exists(host, filepath.Join(config.StateDir(), "sunshine-creds-set")),
			func() error {
				pw, err := secret("sunshine-web-password", c.Stream.WebPassword)
				if err != nil {
					return err
				}
				if _, err := sys.Output(filepath.Join(sunshineDir, "sunshine.exe"), "--creds", c.Stream.WebUser, pw); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(config.StateDir(), "sunshine-creds-set"), nil, 0o600)
			}),
		step(host, "Restart Sunshine", "It reads its config at start.", []string{"sc.exe stop SunshineService", "sc.exe start SunshineService"}, always,
			func() error {
				_, _ = sys.Output("sc.exe", "stop", "SunshineService")
				_, err := sys.Output("sc.exe", "start", "SunshineService")
				return err
			}),
	}
	s = append(s, b.windowsServices(host)...)
	return append(s, b.modSteps(host, filepath.Join(os.Getenv("APPDATA"), "XIVLauncher"), "")...), nil
}

func (b *builder) windowsServices(host plan.Local) []plan.Step {
	c := b.c
	data, _ := c.Encode()
	s := []plan.Step{plan.File(host, config.Path(), data, 0o600, "", "Write "+config.Path(), "The GPU sharing service reads it.")}
	if c.Share.Mode == config.ShareNone {
		return s
	}
	exe := filepath.Join(os.Getenv("ProgramFiles"), "xivstream", "xivstream.exe")
	return append(s,
		step(host, "Install xivstream in Program Files", "", []string{"copy xivstream.exe " + exe}, exists(host, exe),
			func() error {
				self, err := selfBinary()
				if err != nil {
					return err
				}
				return host.WriteFile(exe, self, 0o755, "")
			}),
		step(host, "Register the GPU sharing service", "Gives the game the GPU's memory while it runs ("+c.Share.Mode+").",
			[]string{`sc.exe create xivstream-gpu-share binPath= "\"` + exe + `\" gpu-share" start= auto`, "sc.exe start xivstream-gpu-share"},
			succeeds(host, "sc.exe", "query", "xivstream-gpu-share"),
			func() error {
				if _, err := sys.Output("sc.exe", "create", "xivstream-gpu-share", "binPath=", `"`+exe+`" gpu-share`, "start=", "auto"); err != nil {
					return err
				}
				_, err := sys.Output("sc.exe", "start", "xivstream-gpu-share")
				return err
			}))
}

// macos: Sunshine from LizardByte's Homebrew tap, run by launchd through
// `brew services`; the game through XIV on Mac.
func (b *builder) macos() ([]plan.Step, error) {
	c := b.c
	host := plan.Local{}
	if b.f.Packages != "brew" {
		return nil, fmt.Errorf("Homebrew is needed (https://brew.sh)")
	}
	home, _ := os.UserHomeDir()
	v := b.view(false)
	v.Headless = false
	v.Encoder = "videotoolbox"
	conf := filepath.Join(home, ".config", "sunshine")
	s := []plan.Step{
		step(host, "Install Sunshine", "From LizardByte's Homebrew tap.", []string{"brew tap LizardByte/homebrew", "brew install sunshine"},
			succeeds(host, "sh", "-c", "command -v sunshine"),
			func() error {
				_, err := sys.Output("sh", "-c", "brew tap LizardByte/homebrew && brew install sunshine")
				return err
			}),
		step(host, "Install XIV on Mac", "The macOS launcher, with Dalamud.", []string{"brew install --cask xiv-on-mac"},
			exists(host, "/Applications/XIV on Mac.app"),
			func() error { _, err := sys.Output("brew", "install", "--cask", "xiv-on-mac"); return err }),
		plan.File(host, filepath.Join(conf, "sunshine.conf"), sunshineConf(v), 0o644, "", "Write Sunshine's config", ""),
		plan.File(host, filepath.Join(conf, "apps.json"), []byte(`{
  "env": {},
  "apps": [
    {
      "name": "Final Fantasy XIV",
      "cmd": "open -a 'XIV on Mac'",
      "image-path": "desktop.png"
    }
  ]
}
`), 0o644, "", "Write Sunshine's app list", ""),
		step(host, "Start Sunshine at login (launchd)", "Screen recording and accessibility permissions are granted once in System Settings when macOS asks.",
			[]string{"brew services start sunshine"},
			succeeds(host, "sh", "-c", "brew services list | grep -q '^sunshine .*started'"),
			func() error { _, err := sys.Output("brew", "services", "restart", "sunshine"); return err }),
	}
	if c.Share.Mode != config.ShareNone {
		plist := filepath.Join(home, "Library", "LaunchAgents", "dev.spaceghost.xivstream.gpu-share.plist")
		exe, _ := os.Executable()
		s = append(s, plan.File(host, plist, []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>dev.spaceghost.xivstream.gpu-share</string>
  <key>ProgramArguments</key><array><string>%s</string><string>gpu-share</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict>
</plist>
`, exe)), 0o644, "", "Write the GPU sharing launch agent", ""),
			step(host, "Load the GPU sharing launch agent", "", []string{"launchctl bootstrap gui/$UID " + plist},
				succeeds(host, "launchctl", "list", "dev.spaceghost.xivstream.gpu-share"),
				func() error {
					_, err := sys.Output("sh", "-c", `launchctl bootstrap gui/$(id -u) "$1"`, "-", plist)
					return err
				}))
	}
	data, _ := c.Encode()
	s = append(s, plan.File(host, config.Path(), data, 0o600, "", "Write "+config.Path(), ""))
	return append(s, b.modSteps(host, filepath.Join(home, "Library", "Application Support", "XIV on Mac"), "")...), nil
}
