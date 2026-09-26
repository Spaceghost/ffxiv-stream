// Package config is ffxiv-stream's one configuration file.
//
// The wizard writes it; `apply` reads it. Everything the wizard asks is a key
// here, so a setup can be reproduced (or scripted) without the wizard:
//
//	ffxiv-stream apply --config ./my-setup.toml --yes
//
// Values left empty are detected at apply time (the GPU's render node, the
// Tailscale address, the free VRAM), never frozen at wizard time.
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/BurntSushi/toml"
)

// Topology is where the game runs relative to the machine being set up.
const (
	// TopologyIncus: the game and the streaming server run in an Incus
	// container on this Linux host, with the GPU passed through.
	TopologyIncus = "incus"
	// TopologyHost: the game and the streaming server run directly on this
	// machine (a desktop, a Steam Deck, a Windows or macOS box).
	TopologyHost = "host"
)

// Streaming backends.
const (
	BackendSunshine = "sunshine" // Moonlight clients; Linux, Windows, macOS
	BackendWolf     = "wolf"     // Moonlight clients; Linux, one session per client
	BackendSelkies  = "selkies"  // any browser (WebRTC); Linux
)

// GPU sharing modes.
const (
	ShareNone     = "none"
	ShareReserve  = "reserve"   // reserve VRAM in an almanac gateway while the game runs
	ShareStopUnit = "stop-unit" // stop a service while the game runs (for model servers without a reservation API)
)

type Config struct {
	Topology string `toml:"topology"`
	Backend  string `toml:"backend"`

	Game    Game    `toml:"game"`
	Stream  Stream  `toml:"stream"`
	Incus   Incus   `toml:"incus"`
	Share   Share   `toml:"gpu_share"`
	Audio   Audio   `toml:"audio"`
	Session Session `toml:"session"`
	Selkies Selkies `toml:"selkies"`
	Mods    Mods    `toml:"mods"`
}

type Game struct {
	// Launcher: "xivlauncher" (XIVLauncher.Core on Linux, XIVLauncher on
	// Windows, XIV on Mac on macOS) or "official".
	Launcher string `toml:"launcher"`
	Width    int    `toml:"width"`
	Height   int    `toml:"height"`
	FPS      int    `toml:"fps"`
	// Process is the game's process name, which starts and ends GPU sharing.
	Process string `toml:"process"`
}

type Stream struct {
	Name string `toml:"name"` // what Moonlight shows
	// ListenAddress: the host address clients reach. Empty = this machine's
	// Tailscale IPv4 if it has one, else every address.
	ListenAddress string `toml:"listen_address"`
	// PortBase: Sunshine's base port (47989; the others are offsets from it).
	PortBase int `toml:"port_base"`
	// MaxBitrateKbps caps what the server encodes whatever the client asks.
	MaxBitrateKbps int `toml:"max_bitrate_kbps"`
	// Codecs: "h264" (most compatible) or "auto" (HEVC/AV1 when the client can).
	Codecs string `toml:"codecs"`
	// Gamepad presented to the game: "x360" works everywhere, including Wine
	// without hidraw; "ds5"/"auto" emulate a PlayStation pad (touchpad, PS
	// button), whose hidraw node Incus containers get through the host's udev
	// rule and the input bridge (steps.HidrawHostDir).
	Gamepad string `toml:"gamepad"`
	// WebUser/WebPassword: Sunshine's web UI login. An empty password is
	// generated at apply time and kept in the state directory.
	WebUser     string `toml:"web_user"`
	WebPassword string `toml:"web_password"`
}

type Incus struct {
	Container string `toml:"container"`
	Image     string `toml:"image"`
	CPU       string `toml:"cpu"`    // limits.cpu; empty = no limit
	Memory    string `toml:"memory"` // limits.memory; empty = no limit
	// GPU: PCI address of the card to pass through; empty = the first
	// discrete GPU found at apply time.
	GPU string `toml:"gpu"`
	// Autostart: start the container at boot once the listen address exists.
	Autostart bool `toml:"autostart"`
}

type Share struct {
	Mode string `toml:"mode"`
	// Reserve mode: an almanac gateway reachable from this host, or inside an
	// Incus container (Container set: calls are made from inside it).
	Container string  `toml:"container"`
	Gateway   string  `toml:"gateway"`
	TokenFile string  `toml:"token_file"`
	Owner     string  `toml:"owner"`
	Margin    float64 `toml:"margin"`
	// Stop-unit mode: the unit stopped while the game runs (in Container if set).
	Unit string `toml:"unit"`
	// GraceSeconds after the game exits before the room is given back.
	GraceSeconds int `toml:"grace_seconds"`
}

type Audio struct {
	// StreamOnly: sound only leaves through the stream; no local sinks.
	StreamOnly bool `toml:"stream_only"`
}

type Selkies struct {
	Port     int    `toml:"port"`
	User     string `toml:"user"`
	Password string `toml:"password"` // empty = generated at apply time, kept in the state directory
}

// Mods are Dalamud plugins installed for the game, from plugin repositories.
type Mods struct {
	// Repos: Dalamud third-party repository URLs (plugins.json). The wizard
	// lists what they offer; apply adds them to Dalamud.
	Repos []string `toml:"repos"`
	// Install: plugin InternalNames to install and enable.
	Install []string `toml:"install"`
	// Testing: install each plugin's testing version where it has one.
	Testing bool `toml:"testing"`
	// Companions: install what a plugin needs outside the game (ghostty-agent
	// for GhosttyDalamud, the almanac gateway link for Almanac).
	Companions bool `toml:"companions"`
}

type Session struct {
	// Headless: a dedicated headless compositor session (the only option in a
	// container; on a host, false streams the existing desktop instead).
	Headless bool `toml:"headless"`
	// User the session runs as (created if missing; on a host with an
	// existing desktop, your own user).
	User string `toml:"user"`
}

// DefaultModRepo is the ffxiv-stream family's own Dalamud repository.
const DefaultModRepo = "https://spacegho.st/mods/ffxiv/plugins.json"

// Default is a setup that works on a single-GPU Linux host with Incus, and on
// other systems the game and Sunshine on this machine.
func Default() Config {
	topology := TopologyIncus
	if runtime.GOOS != "linux" {
		topology = TopologyHost
	}
	return Config{
		Topology: topology,
		Backend:  BackendSunshine,
		Game:     Game{Launcher: "xivlauncher", Width: 1920, Height: 1080, FPS: 60, Process: "ffxiv_dx11.exe"},
		Stream: Stream{
			Name: "ffxiv", PortBase: 47989, MaxBitrateKbps: 50000, Codecs: "h264", Gamepad: "x360", WebUser: "ffxiv",
		},
		Incus: Incus{Container: "ffxiv", Image: "images:fedora/43", Autostart: true},
		Share: Share{
			Mode: ShareNone, Gateway: "http://127.0.0.1:41881", Owner: "ffxiv", Margin: 1.25, GraceSeconds: 20,
		},
		Audio:   Audio{StreamOnly: true},
		Session: Session{Headless: true, User: "player"},
		Selkies: Selkies{Port: 8080, User: "ffxiv"},
		Mods:    Mods{Repos: []string{DefaultModRepo}, Companions: true},
	}
}

// Path is the default config location for this OS.
func Path() string {
	if p := os.Getenv("FFXIV_STREAM_CONFIG"); p != "" {
		return p
	}
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(os.Getenv("ProgramData"), "ffxiv-stream", "config.toml")
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", "ffxiv-stream", "config.toml")
	default:
		if os.Geteuid() == 0 {
			return "/etc/ffxiv-stream/config.toml"
		}
		dir, err := os.UserConfigDir()
		if err != nil {
			dir = "."
		}
		return filepath.Join(dir, "ffxiv-stream", "config.toml")
	}
}

// StateDir holds what apply generates and must keep (the web password).
func StateDir() string {
	if p := os.Getenv("STATE_DIRECTORY"); p != "" {
		return p
	}
	switch runtime.GOOS {
	case "windows", "darwin":
		return filepath.Dir(Path())
	default:
		if os.Geteuid() == 0 {
			return "/var/lib/ffxiv-stream"
		}
		dir, err := os.UserHomeDir()
		if err != nil {
			return "."
		}
		return filepath.Join(dir, ".local", "state", "ffxiv-stream")
	}
}

// Load reads path over the defaults, so a file may set only what differs.
func Load(path string) (Config, error) {
	c := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	md, err := toml.Decode(string(data), &c)
	if err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		return c, fmt.Errorf("%s: unknown keys %v", path, undecoded)
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	switch c.Topology {
	case TopologyIncus, TopologyHost:
	default:
		return fmt.Errorf("topology %q: want %q or %q", c.Topology, TopologyIncus, TopologyHost)
	}
	switch c.Backend {
	case BackendSunshine, BackendWolf, BackendSelkies:
	default:
		return fmt.Errorf("backend %q: want sunshine, wolf or selkies", c.Backend)
	}
	if c.Backend != BackendSunshine && runtime.GOOS != "linux" {
		return fmt.Errorf("backend %q runs on Linux hosts only; use sunshine on %s", c.Backend, runtime.GOOS)
	}
	if c.Topology == TopologyIncus && runtime.GOOS != "linux" {
		return fmt.Errorf("topology incus needs a Linux host")
	}
	if c.Backend == BackendWolf && c.Topology == TopologyIncus {
		return fmt.Errorf("wolf runs its sessions as containers itself; use topology = %q with it", TopologyHost)
	}
	switch c.Share.Mode {
	case ShareNone, ShareReserve, ShareStopUnit:
	default:
		return fmt.Errorf("gpu_share.mode %q: want none, reserve or stop-unit", c.Share.Mode)
	}
	if c.Share.Mode == ShareStopUnit && c.Share.Unit == "" {
		return fmt.Errorf("gpu_share.mode = stop-unit needs gpu_share.unit")
	}
	switch c.Stream.Codecs {
	case "h264", "auto":
	default:
		return fmt.Errorf("stream.codecs %q: want h264 or auto", c.Stream.Codecs)
	}
	if c.Game.Width <= 0 || c.Game.Height <= 0 || c.Game.FPS <= 0 {
		return fmt.Errorf("game width, height and fps must be positive")
	}
	return nil
}

// Encode renders the config as TOML, with a header saying where it came from.
func (c Config) Encode() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("# ffxiv-stream configuration. Written by `ffxiv-stream wizard`; apply it with\n")
	buf.WriteString("# `ffxiv-stream apply`. Empty values are detected at apply time.\n\n")
	if err := toml.NewEncoder(&buf).Encode(c); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Save writes the config atomically, creating its directory.
func (c Config) Save(path string) error {
	data, err := c.Encode()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
