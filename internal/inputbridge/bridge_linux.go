// Package inputbridge announces the streaming server's virtual input devices
// to a container's udev (`ffxiv-stream input-bridge`, run inside the guest).
//
// Sunshine creates the Moonlight client's keyboard, mice and gamepads through
// /dev/uinput and /dev/uhid. The kernel puts their event nodes in the host's
// /dev/input, which is bind-mounted into the container, but it sends the
// uevents only to the host. Without an "add" event the container's udevd never
// records the devices, and libinput (sway) only uses devices udev has
// recorded, so the stream would have no input.
//
// This watches /dev/input and, for the server's devices, injects the add or
// remove uevent into the container's network namespace, which the kernel then
// re-broadcasts as its own. That is the mechanism Incus uses for hotplugged
// devices (unicast to the kernel on NETLINK_KOBJECT_UEVENT; needs
// CAP_SYS_ADMIN in the container). Incus's own unix-hotplug device is not used
// because in Incus 6.23 it deadlocks incusd when a container stops while a
// matched device is being removed.
package inputbridge

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Marks are substrings of the device names the streaming servers give their
// virtual devices: Sunshine's "libvirtualhid Keyboard" and "Sunshine
// (libvirtualhid) X-Box 360 Controller"; Selkies' and Wolf's "Wolf ... virtual"
// style names. Nothing else is announced, so the container never sees the
// host's own keyboards.
var Marks = []string{"libvirtualhid", "Selkies", "Wolf"}

type Bridge struct {
	InputDir string
	SysDir   string // /sys
	UdevData string // /run/udev/data
	Marks    []string
	inputGID int
	nl       int
	known    map[string]map[string]string // event node -> uevent properties
}

func New() (*Bridge, error) {
	b := &Bridge{InputDir: "/dev/input", SysDir: "/sys", UdevData: "/run/udev/data", Marks: Marks, known: map[string]map[string]string{}}
	g, err := user.LookupGroup("input")
	if err != nil {
		return nil, fmt.Errorf("group input: %w", err)
	}
	b.inputGID, _ = strconv.Atoi(g.Gid)
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW, unix.NETLINK_KOBJECT_UEVENT)
	if err != nil {
		return nil, fmt.Errorf("netlink socket: %w", err)
	}
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return nil, fmt.Errorf("netlink bind: %w", err)
	}
	b.nl = fd
	return b, nil
}

func (b *Bridge) Run() error {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC)
	if err != nil {
		return fmt.Errorf("inotify: %w", err)
	}
	if _, err := unix.InotifyAddWatch(fd, b.InputDir, unix.IN_CREATE|unix.IN_DELETE); err != nil {
		return fmt.Errorf("inotify on %s: %w", b.InputDir, err)
	}
	entries, _ := os.ReadDir(b.InputDir)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names { // devices that predate us
		b.added(n, true)
	}
	buf := make([]byte, 64*1024)
	for {
		n, err := unix.Read(fd, buf)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			return fmt.Errorf("inotify read: %w", err)
		}
		for i := 0; i+unix.SizeofInotifyEvent <= n; {
			ev := (*unix.InotifyEvent)(unsafePointer(&buf[i]))
			nameBytes := buf[i+unix.SizeofInotifyEvent : i+unix.SizeofInotifyEvent+int(ev.Len)]
			name := strings.TrimRight(string(nameBytes), "\x00")
			i += unix.SizeofInotifyEvent + int(ev.Len)
			switch {
			case ev.Mask&unix.IN_CREATE != 0:
				b.added(name, false)
			case ev.Mask&unix.IN_DELETE != 0:
				b.removed(name)
			}
		}
	}
}

// describe returns the uevent properties of a streamed event node, or nil.
func (b *Bridge) describe(node string) map[string]string {
	sysdir := filepath.Join(b.SysDir, "class", "input", node)
	var name, uevent string
	for i := 0; ; i++ { // the node can appear a moment before sysfs settles
		n, err1 := os.ReadFile(filepath.Join(sysdir, "device", "name"))
		u, err2 := os.ReadFile(filepath.Join(sysdir, "uevent"))
		if err1 == nil && err2 == nil {
			name, uevent = strings.TrimSpace(string(n)), string(u)
			break
		}
		if i >= 50 {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !b.matches(name) {
		return nil
	}
	props := map[string]string{}
	for _, line := range strings.Fields(uevent) {
		if k, v, ok := strings.Cut(line, "="); ok {
			props[k] = v
		}
	}
	real, err := filepath.EvalSymlinks(sysdir)
	if err != nil {
		return nil
	}
	props["DEVPATH"] = strings.TrimPrefix(real, b.SysDir)
	props["SUBSYSTEM"] = "input"
	props["NAME"] = name
	return props
}

func (b *Bridge) matches(name string) bool {
	for _, m := range b.Marks {
		if strings.Contains(name, m) {
			return true
		}
	}
	return false
}

func (b *Bridge) added(node string, startup bool) {
	if !strings.HasPrefix(node, "event") || b.known[node] != nil {
		return
	}
	props := b.describe(node)
	if props == nil {
		return
	}
	b.known[node] = props
	// After a restart of this service, devices announced by the previous run
	// are already in udev (and open in libinput); a second "add" would give
	// sway duplicate keyboards and mice.
	if startup && os.Getenv("FFXIV_STREAM_REANNOUNCE") == "" {
		if _, err := os.Stat(filepath.Join(b.UdevData, "c"+props["MAJOR"]+":"+props["MINOR"])); err == nil {
			return
		}
	}
	b.waitUntilOurs(node)
	if err := b.inject("add", props); err != nil {
		log.Printf("add %s: %v", props["DEVNAME"], err)
	}
}

func (b *Bridge) removed(node string) {
	props := b.known[node]
	if props == nil {
		return
	}
	delete(b.known, node)
	if err := b.inject("remove", props); err != nil {
		log.Printf("remove %s: %v", props["DEVNAME"], err)
	}
}

// waitUntilOurs: the host's udev hands each node to the container with a chown
// that lands a little after the node appears. Announcing before it would have
// libinput try the node while it is still unreadable here, and give up.
func (b *Bridge) waitUntilOurs(node string) {
	path := filepath.Join(b.InputDir, node)
	for i := 0; i < 250; i++ {
		var st unix.Stat_t
		if err := unix.Stat(path, &st); err != nil {
			return
		}
		if int(st.Gid) == b.inputGID {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	log.Printf("warning: %s never became group input; is the host's ffxiv-stream udev rule installed?", path)
}

// Message builds the netlink message the kernel re-broadcasts as a uevent.
// The kernel runs injected messages through netlink_rcv_skb, which silently
// skips anything without a request header of at least NLMSG_MIN_TYPE; asking
// for an ACK is how a refusal (e.g. missing CAP_SYS_ADMIN) becomes visible.
func Message(action string, props map[string]string, seqnum string) []byte {
	keys := make([]string, 0, len(props))
	for k := range props {
		if k != "NAME" && k != "ACTION" && k != "SEQNUM" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var body strings.Builder
	body.WriteString(action + "@" + props["DEVPATH"] + "\x00")
	body.WriteString("ACTION=" + action + "\x00")
	for _, k := range keys {
		body.WriteString(k + "=" + props[k] + "\x00")
	}
	body.WriteString("SEQNUM=" + seqnum + "\x00")
	payload := []byte(body.String())
	msg := make([]byte, 16+len(payload))
	binary.NativeEndian.PutUint32(msg[0:], uint32(len(msg)))
	binary.NativeEndian.PutUint16(msg[4:], 0x10)                              // NLMSG_MIN_TYPE
	binary.NativeEndian.PutUint16(msg[6:], unix.NLM_F_REQUEST|unix.NLM_F_ACK) // flags
	copy(msg[16:], payload)
	return msg
}

func (b *Bridge) inject(action string, props map[string]string) error {
	seq := strings.TrimSpace(readFile(filepath.Join(b.SysDir, "kernel", "uevent_seqnum")))
	if err := unix.Sendto(b.nl, Message(action, props, seq), 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return err
	}
	ack := make([]byte, 4096)
	n, _, err := unix.Recvfrom(b.nl, ack, 0)
	if err != nil {
		return err
	}
	if n >= 20 {
		if code := int32(binary.NativeEndian.Uint32(ack[16:])); code != 0 {
			return fmt.Errorf("kernel refused the uevent: %w", syscall.Errno(-code))
		}
	}
	log.Printf("%s %s (%s)", action, props["DEVNAME"], props["NAME"])
	return nil
}

func readFile(path string) string {
	data, _ := os.ReadFile(path)
	return string(data)
}
