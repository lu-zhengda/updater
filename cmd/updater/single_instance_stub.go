//go:build !darwin

package main

import "time"

// listOtherUpdaterInstances is unavailable off macOS.
func listOtherUpdaterInstances(selfPID int) []otherMenubarInstance {
	_ = selfPID
	return nil
}

func activateRunningApplication(pid int) {
	_ = pid
}

func terminateMenubarInstance(pid int, timeout time.Duration) {
	_ = pid
	_ = timeout
}
