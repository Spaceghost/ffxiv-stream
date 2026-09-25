# Streaming backends

ffxiv-stream sets up one of three streaming servers. This page is what each one
needs and how ffxiv-stream sets it up, with the upstream sources it follows
(checked 2026-09-23).

| | Sunshine | Wolf | Selkies |
|---|---|---|---|
| Clients | Moonlight (every platform) | Moonlight | any web browser |
| Host OS | Linux, Windows, macOS | Linux | Linux |
| Sessions | one, shared | one private session per client | one, shared |
| ffxiv-stream topologies | incus, host | host | incus, host |
| Status in ffxiv-stream | tested (NVIDIA, Incus) | experimental | starts and serves; picture unchecked |
| License | GPL-3.0 | MIT | MPL-2.0 |

## Sunshine

<https://github.com/LizardByte/Sunshine>

What ffxiv-stream does:

- **Linux headless (Incus or host).** The session is a headless sway, which implements
  wlr-screencopy (`capture = wlr`); gamescope does not, and a container has no scanout
  for kmsgrab. Sunshine is started by sway so it inherits the right `WAYLAND_DISPLAY`,
  and `adapter_name` is set at every start to the streaming GPU's render node.
- **NVIDIA.** `dmabuf-wait` is preloaded into Sunshine. NVIDIA's EGL ignores the implicit
  fence on screencopy dma-bufs, so under load Sunshine read frames sway had not finished
  copying, and the stream flickered black. The shim waits on the dma-buf before
  `eglCreateImage`. Sunshine's binary has file capabilities, so glibc runs it in secure-exec
  mode, where only a bare library name from the system directory is preloaded, set-user-ID.
- **Incus port forwards** use `nat=true`. Incus's userspace proxy dropped over 400k UDP
  video packets in a session, which looked like flicker.
- **Input in a container.** Sunshine's virtual devices appear in the host's `/dev/input`. A
  host udev rule hands just those nodes to the container's user, and `ffxiv-stream
  input-bridge` inside the container injects their uevents, so the container's udev and
  libinput see them. Incus's own `unix-hotplug` device is not used: in Incus 6.23 it deadlocks
  incusd.
- **Codecs and bitrate.** The defaults are H.264 only (some Linux clients show cycling colours
  on hardware HEVC decode) and a 50 Mbit/s cap (Wi-Fi clients lose packets above what their link
  carries). Both can be changed in the wizard's advanced mode.
- **Pads.** They are presented as Xbox 360 pads (`gamepad = x360`), which Wine reads over plain
  evdev; DualSense emulation needs hidraw.
- **Install routes.**
  - Fedora: LizardByte's Copr.
  - Debian/Ubuntu and Arch: the release packages.
  - Bazzite: its bundled Sunshine.
  - SteamOS and other read-only systems: Flathub's `dev.lizardbyte.app.Sunshine`.
  - Windows: `winget install LizardByte.Sunshine`.
  - macOS: `brew install lizardbyte/homebrew/sunshine`.

## Wolf

<https://github.com/games-on-whales/wolf>, docs at <https://games-on-whales.github.io/wolf/stable/>

- There is no versioned release after `v2024.07`. The image is `ghcr.io/games-on-whales/wolf:stable`,
  built from the `stable` branch.
- **Rootful only.** Rootless Podman starts Wolf, but launching an app fails: "device cgroup rules
  are not supported in rootless mode" (issue #477). ffxiv-stream writes a rootful Quadlet
  (`/etc/containers/systemd/wolf.container`) with host networking, the Podman socket mounted as
  `docker.sock`, `/dev` and `/run/udev`, `--ipc=host` and `--device-cgroup-rule "c 13:* rmw"`.
- **Host setup.** Wolf's `85-wolf.rules` (installed as is from upstream, so streamed pads get
  their own seat and cannot drive the host desktop), plus `uhid` in `modules-load.d`.
- **NVIDIA.** ffxiv-stream uses CDI (`nvidia.com/gpu=all`, nvidia-container-toolkit ≥ 1.16).
  Wolf's docs call their driver-volume route steadier (issue #152); set it up by hand if CDI
  misbehaves. `nvidia-drm.modeset=1` is required either way.
- **Apps are containers.** ffxiv-stream's app image, `ghcr.io/spaceghost/ffxiv-stream-xivlauncher`
  (see `images/xivlauncher`), is XIVLauncher.Core on GoW's `base-app`. Wolf reads
  `/etc/wolf/cfg/config.toml` at start and rewrites it only when a client pairs.
- **Pairing.** Moonlight shows a PIN, and Wolf logs `Insert pin at http://<ip>:47989/pin/#<secret>`.
  The API on `/var/run/wolf/wolf.sock` also works: `GET /api/v1/pair/pending`, then
  `POST /api/v1/pair/client {pair_secret, pin}`.
- **Not in an Incus container.** Wolf's docs say it runs in an LXC container only when that
  container is privileged. ffxiv-stream therefore offers Wolf on the host only.

## Selkies

<https://github.com/selkies-project/selkies>

- **Version 2.0.0 (2026-09-23) dropped GStreamer.** Guides for selkies-gstreamer 1.x
  (`nvh264enc`, WebRTC by default) no longer apply.
- **Install routes.** Release packages for Fedora (`-fc-x86_64.rpm`), Debian/Ubuntu, Arch and
  Alpine; an AppImage; `pip install selkies`; and container images.
- **What ffxiv-stream runs** inside the headless sway:
  `selkies --wayland=true --wayland-host-display=$WAYLAND_DISPLAY --public --port=8080 --enable-https=true --encoder=h264enc`.
  `--wayland-host-display` only takes effect with `--wayland=true`. Without it, Selkies captures X11,
  which here is sway's rootless Xwayland. Keyboard and mouse go through sway's virtual-keyboard and
  virtual-pointer protocols, not uinput. Selkies refuses to start with basic auth and no password,
  so a password is generated, kept in the state directory, and passed as `SELKIES_BASIC_AUTH_PASSWORD`
  from a mode-600 file, never on the command line.
- **Encoders.** `--encoder=h264enc` picks NVENC or VA-API where the GPU has it.
- **HTTPS matters.** Browsers allow gamepads, clipboard and pointer lock only in a secure
  context. Selkies makes a self-signed certificate on first start.
- **Transport.** The default is WebSockets on one TCP port, with no STUN/TURN needed on a LAN
  or a tailnet. WebRTC (`--mode=webrtc`) needs UDP 49152–65535 or a TURN server.
- **Tested 2026-09-24** in an Incus container on Fedora with an NVIDIA P4000 (Selkies 2.0.0 from the
  `-fc-x86_64.rpm`): it installs and starts, finds NVENC ("Render node 1 encodes H264, H265 on nvenc"),
  reports "Wayland (host compositor 'wayland-1') capture", and answers 401 without the login and 200
  with it. The picture in a real browser has not been checked yet.
