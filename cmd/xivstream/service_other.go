//go:build !windows

package main

// runService runs a long-lived service in the foreground; systemd, OpenRC and
// launchd supervise it directly.
func runService(name string, run func() error, stop func()) error { return run() }
