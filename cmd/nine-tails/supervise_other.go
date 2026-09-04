//go:build !unix

package main

import "os"

func supervisorSignals() []os.Signal { return []os.Signal{os.Interrupt} }

func forwardSupervisorSignal(os.Signal) bool { return false }
