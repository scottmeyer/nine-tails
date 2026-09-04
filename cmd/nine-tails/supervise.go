package main

import (
	"os"
	"os/exec"
	"os/signal"
	"time"
)

// runHarnessChild keeps the wrapper alive when a foreground terminal sends an
// interrupt to both processes. Interactive harnesses use that interrupt to
// cancel the current turn and commonly continue running. Termination signals
// are forwarded, with a bounded grace period so wrapper cleanup still runs.
func runHarnessChild(child *exec.Cmd) error {
	signals := supervisorSignals()
	signalCh := make(chan os.Signal, len(signals)+1)
	signal.Notify(signalCh, signals...)
	defer signal.Stop(signalCh)

	if err := child.Start(); err != nil {
		return err
	}
	waitCh := make(chan error, 1)
	go func() { waitCh <- child.Wait() }()

	var force <-chan time.Time
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case err := <-waitCh:
			return err
		case sig := <-signalCh:
			if !forwardSupervisorSignal(sig) {
				continue
			}
			if timer != nil {
				_ = child.Process.Kill()
				continue
			}
			_ = child.Process.Signal(sig)
			timer = time.NewTimer(5 * time.Second)
			force = timer.C
		case <-force:
			_ = child.Process.Kill()
			force = nil
		}
	}
}
