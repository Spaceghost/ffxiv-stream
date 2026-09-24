// ffxiv-stream sets up FINAL FANTASY XIV game streaming (Sunshine, Wolf or
// Selkies) with its mods, and runs the small services that keep it working.
//
//	ffxiv-stream                  the wizard (or `wizard`)
//	ffxiv-stream detect           what this machine is and has
//	ffxiv-stream plan [-v]        what apply would do (nothing changes)
//	ffxiv-stream apply [--yes]    do it
//	ffxiv-stream doctor           check a setup
//	ffxiv-stream pair PIN [NAME]  pair a Moonlight client
//
// Services (started by the units apply installs):
//
//	gpu-share, input-bridge, prepare-gpu, render-node, start-container
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/Spaceghost/ffxiv-stream/internal/config"
	"github.com/Spaceghost/ffxiv-stream/internal/detect"
	"github.com/Spaceghost/ffxiv-stream/internal/gpuprep"
	"github.com/Spaceghost/ffxiv-stream/internal/gpushare"
	"github.com/Spaceghost/ffxiv-stream/internal/inputbridge"
	"github.com/Spaceghost/ffxiv-stream/internal/plan"
	"github.com/Spaceghost/ffxiv-stream/internal/steps"
	"github.com/Spaceghost/ffxiv-stream/internal/sunshine"
	"github.com/Spaceghost/ffxiv-stream/internal/sys"
	"github.com/Spaceghost/ffxiv-stream/internal/wizard"
)

// version is set by the release build (-ldflags "-X main.version=...").
var version = "dev"

func main() {
	log.SetFlags(0)
	args := os.Args[1:]
	cmd := "wizard"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "wizard":
		err = runWizard(args)
	case "detect":
		err = runDetect(args)
	case "plan":
		err = runPlan(args)
	case "apply":
		err = runApply(args)
	case "doctor":
		err = runDoctor(args)
	case "pair":
		err = runPair(args)
	case "gpu-share":
		err = runGPUShare(args)
	case "input-bridge":
		var b *inputbridge.Bridge
		if b, err = inputbridge.New(); err == nil {
			err = b.Run()
		}
	case "prepare-gpu":
		err = gpuprep.Prepare()
	case "render-node":
		err = runRenderNode()
	case "start-container":
		err = runStartContainer(args)
	case "version", "--version", "-V":
		fmt.Println("ffxiv-stream", version)
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ffxiv-stream:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `ffxiv-stream: FINAL FANTASY XIV game streaming, set up for you.

  ffxiv-stream [wizard] [--advanced]   ask a few questions, show the plan, apply it
  ffxiv-stream detect [--json]         what this machine is and has
  ffxiv-stream plan [-v]               what apply would do; nothing is changed
  ffxiv-stream apply [--yes]           set up (or bring up to date) from the config
  ffxiv-stream doctor                  check the setup
  ffxiv-stream pair PIN [NAME]         pair a Moonlight client showing PIN
  ffxiv-stream version

  --config PATH   use this config (default `+config.Path()+`)

Services (run by the units apply installs):
  gpu-share [--stop]   give the game the GPU's memory while it runs
  input-bridge         announce the stream's input devices inside a container
  prepare-gpu          fix the passed-through GPU's manifests and nodes
  render-node          print the streaming GPU's render node
  start-container      start the Incus container once its stream address exists
`)
}

func configFlag(fs *flag.FlagSet) *string {
	return fs.String("config", config.Path(), "configuration file")
}

func load(path string) (config.Config, error) {
	c, err := config.Load(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, fmt.Errorf("no configuration at %s yet: run `ffxiv-stream wizard` first", path)
	}
	return c, err
}

func runWizard(args []string) error {
	fs := flag.NewFlagSet("wizard", flag.ExitOnError)
	path := configFlag(fs)
	advanced := fs.Bool("advanced", false, "also ask what most people never change")
	_ = fs.Parse(args)
	start, err := config.Load(*path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f := detect.Run()
	c, result, err := wizard.Run(start, f, *advanced, func(c config.Config) string { return review(c, f) })
	if err != nil {
		return err
	}
	switch result {
	case wizard.Cancel:
		fmt.Println("Nothing was changed.")
		return nil
	case wizard.SaveOnly:
		if err := c.Save(*path); err != nil {
			return err
		}
		fmt.Printf("Saved %s. Apply it with: sudo ffxiv-stream apply --config %s\n", *path, *path)
		return nil
	}
	if err := c.Save(*path); err != nil {
		return err
	}
	return apply(c, f)
}

// review is the wizard's last screen: the config and the plan, verbatim.
func review(c config.Config, f detect.Facts) string {
	data, _ := c.Encode()
	var b strings.Builder
	b.WriteString(string(data))
	b.WriteString("\n---- plan ----\n")
	s, err := steps.Build(c, f)
	if err != nil {
		return b.String() + "cannot plan: " + err.Error()
	}
	plan.Print(&b, plan.Evaluate(s), true)
	return b.String()
}

func runDetect(args []string) error {
	fs := flag.NewFlagSet("detect", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable")
	_ = fs.Parse(args)
	f := detect.Run()
	if *asJSON {
		out, _ := json.MarshalIndent(f, "", "  ")
		fmt.Println(string(out))
		return nil
	}
	out, _ := json.MarshalIndent(f, "", "  ")
	fmt.Println(string(out))
	return nil
}

func runPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	path := configFlag(fs)
	verbose := fs.Bool("v", false, "show commands, reasons and file diffs")
	_ = fs.Parse(args)
	c, err := load(*path)
	if err != nil {
		return err
	}
	s, err := steps.Build(c, detect.Run())
	if err != nil {
		return err
	}
	plan.Print(os.Stdout, plan.Evaluate(s), *verbose)
	return nil
}

func runApply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ExitOnError)
	path := configFlag(fs)
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	_ = fs.Parse(args)
	c, err := load(*path)
	if err != nil {
		return err
	}
	f := detect.Run()
	if !*yes {
		s, err := steps.Build(c, f)
		if err != nil {
			return err
		}
		plan.Print(os.Stdout, plan.Evaluate(s), false)
		fmt.Print("\nApply? [y/N] ")
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "y") {
			fmt.Println("Nothing was changed.")
			return nil
		}
	}
	return apply(c, f)
}

func apply(c config.Config, f detect.Facts) error {
	if f.OS == "linux" && !f.Root {
		return errors.New("apply changes system files and services: run it with sudo")
	}
	s, err := steps.Build(c, f)
	if err != nil {
		return err
	}
	if err := plan.Apply(os.Stdout, s); err != nil {
		return err
	}
	fmt.Println("\nDone.")
	for _, name := range []string{"sunshine-web-password", "selkies-password"} {
		if data, err := os.ReadFile(steps.SecretPath(name)); err == nil {
			fmt.Printf("%s: %s (kept in %s)\n", name, strings.TrimSpace(string(data)), steps.SecretPath(name))
		}
	}
	switch c.Backend {
	case config.BackendSunshine, config.BackendWolf:
		fmt.Println("Pair a client: add this machine in Moonlight, then run `ffxiv-stream pair <PIN>` with the PIN it shows.")
	case config.BackendSelkies:
		fmt.Printf("Open https://<this machine>:%d in a browser.\n", c.Selkies.Port)
	}
	return nil
}

func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	path := configFlag(fs)
	_ = fs.Parse(args)
	c, err := load(*path)
	if err != nil {
		return err
	}
	f := detect.Run()
	s, err := steps.Build(c, f)
	if err != nil {
		return err
	}
	entries := plan.Evaluate(s)
	plan.Print(os.Stdout, entries, false)
	for _, e := range entries {
		if e.State != plan.Done {
			return errors.New("the setup is incomplete or out of date: `ffxiv-stream apply` brings it up to date")
		}
	}
	fmt.Println("Everything is in place.")
	return nil
}

func runPair(args []string) error {
	fs := flag.NewFlagSet("pair", flag.ExitOnError)
	path := configFlag(fs)
	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		return errors.New("usage: ffxiv-stream pair PIN [NAME]")
	}
	c, err := load(*path)
	if err != nil {
		return err
	}
	name := "moonlight"
	if fs.NArg() > 1 {
		name = fs.Arg(1)
	}
	if c.Backend != config.BackendSunshine {
		return fmt.Errorf("pair supports Sunshine; for %s use its web page", c.Backend)
	}
	pw, err := os.ReadFile(steps.SecretPath("sunshine-web-password"))
	if c.Stream.WebPassword != "" {
		pw, err = []byte(c.Stream.WebPassword), nil
	}
	if err != nil {
		return fmt.Errorf("no Sunshine web password (set stream.web_password or run apply): %w", err)
	}
	var t plan.Target = plan.Local{}
	if c.Topology == config.TopologyIncus {
		t = plan.Incus{Container: c.Incus.Container}
	}
	if err := sunshine.Pair(t, c.Stream.PortBase+1, c.Stream.WebUser, strings.TrimSpace(string(pw)), fs.Arg(0), name); err != nil {
		return err
	}
	fmt.Println("Paired.")
	return nil
}

func runGPUShare(args []string) error {
	fs := flag.NewFlagSet("gpu-share", flag.ExitOnError)
	path := configFlag(fs)
	stop := fs.Bool("stop", false, "give the room back (service stop hook)")
	_ = fs.Parse(args)
	c, err := load(*path)
	if err != nil {
		return err
	}
	s := gpushare.New(c)
	if *stop {
		s.Stop()
		return nil
	}
	return runService("ffxiv-stream-gpu-share", s.Run, s.Stop)
}

func runRenderNode() error {
	f := detect.Run()
	gpu, ok := f.PrimaryGPU()
	if !ok || gpu.RenderNode == "" {
		return errors.New("no GPU render node")
	}
	fmt.Println(gpu.RenderNode)
	return nil
}

// runStartContainer waits for the address the stream's forwards listen on
// (Tailscale's comes up late in boot, and Incus refuses to start a container
// whose forward cannot bind), then starts the container.
func runStartContainer(args []string) error {
	fs := flag.NewFlagSet("start-container", flag.ExitOnError)
	path := configFlag(fs)
	_ = fs.Parse(args)
	c, err := load(*path)
	if err != nil {
		return err
	}
	name := c.Incus.Container
	listen, _ := sys.Output("incus", "config", "device", "get", name, "stream-http", "listen")
	parts := strings.Split(listen, ":") // tcp:ADDR:PORT
	if len(parts) == 3 && parts[1] != "0.0.0.0" {
		for i := 0; ; i++ {
			out, _ := sys.Output("ip", "-o", "addr", "show")
			if strings.Contains(out, " "+parts[1]+"/") {
				break
			}
			if i > 300 {
				return fmt.Errorf("%s never appeared; not starting %s", parts[1], name)
			}
			time.Sleep(time.Second)
		}
	}
	state, _ := sys.Output("incus", "list", "^"+name+"$", "-c", "s", "-f", "csv")
	if state == "RUNNING" {
		return nil
	}
	_, err = sys.Output("incus", "start", name)
	return err
}
