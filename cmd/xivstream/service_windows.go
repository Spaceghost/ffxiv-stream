//go:build windows

package main

import (
	"golang.org/x/sys/windows/svc"
)

// runService speaks the Service Control Manager protocol when started as a
// Windows service, and runs in the foreground otherwise.
func runService(name string, run func() error, stop func()) error {
	inService, err := svc.IsWindowsService()
	if err != nil || !inService {
		return run()
	}
	return svc.Run(name, handler{run: run, stop: stop})
}

type handler struct {
	run  func() error
	stop func()
}

func (h handler) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	done := make(chan error, 1)
	go func() { done <- h.run() }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			if err != nil {
				return true, 1
			}
			return false, 0
		case r := <-requests:
			switch r.Cmd {
			case svc.Interrogate:
				status <- r.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				h.stop()
				return false, 0
			}
		}
	}
}
