# Configuration

`xivstream wizard` writes this file; `xivstream apply` reads it. Keys you
leave out take the defaults shown. Empty values are detected when apply runs.

Location: `/etc/xivstream/config.toml` (root on Linux), `~/.config/xivstream/config.toml`
(other users), `%ProgramData%\xivstream\config.toml` (Windows),
`~/Library/Application Support/xivstream/config.toml` (macOS), or `--config PATH` /
`$XIVSTREAM_CONFIG`.

```toml
topology = "incus"            # "incus": an Incus container on this Linux host; "host": this machine
backend  = "sunshine"         # "sunshine", "wolf" (Linux host, experimental), "selkies" (Linux, experimental)

[game]
launcher = "xivlauncher"
width = 1920
height = 1080
fps = 60                      # also DXVK's frame cap
process = "ffxiv_dx11.exe"    # its presence starts and ends GPU sharing

[stream]
name = "ffxiv"                # the name Moonlight shows
listen_address = ""           # "" = this host's Tailscale IPv4, else its primary address
port_base = 47989             # Moonlight's layout: https -5, webui +1, video +9, control +10, audio +11, mic +13, rtsp +21
max_bitrate_kbps = 50000      # a cap whatever the client asks for
codecs = "h264"               # "h264" or "auto" (HEVC/AV1 when the client can)
gamepad = "x360"              # "x360", "ds5", "auto"
web_user = "ffxiv"            # Sunshine's web UI
web_password = ""             # "" = generated once, kept in the state directory

[session]
headless = true               # a dedicated headless sway; false streams your desktop (host only)
user = "player"               # the session user (created if missing)

[incus]                       # topology = "incus"
container = "ffxiv"
image = "images:fedora/43"
cpu = ""                      # limits.cpu, "" = unlimited
memory = ""                   # limits.memory, "" = unlimited
gpu = ""                      # PCI address, "" = the first discrete GPU
autostart = true              # start at boot once the listen address exists

[selkies]                     # backend = "selkies"
port = 8080
user = "ffxiv"
password = ""                 # "" = generated once

[audio]
stream_only = true            # nothing plays aloud on this machine

[gpu_share]
mode = "none"                 # "reserve" (almanac), "stop-unit" (e.g. Ollama), "none"
container = ""                # Incus container the model server runs in, "" = this machine
gateway = "http://127.0.0.1:41881"
token_file = ""               # almanac's gateway token
owner = "ffxiv"               # the reservation's name in almanac
margin = 1.25                 # reserve the game's peak VRAM use times this
unit = ""                     # stop-unit: the service to stop while playing
grace_seconds = 20            # after the game exits, before the room is given back

[mods]
repos = ["https://spacegho.st/mods/ffxiv/plugins.json"]
install = []                  # plugin InternalNames, e.g. ["GhosttyDalamud", "Almanac"]
testing = false               # testing versions where a plugin has one
companions = true             # e.g. ghostty-agent for GhosttyDalamud
```

State directory (generated passwords, the game's measured VRAM peak):
`/var/lib/xivstream` as root, `~/.local/state/xivstream` otherwise.
