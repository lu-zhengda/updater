//go:build !darwin

package main

// listOtherUpdaterInstances is unavailable off macOS.
func listOtherUpdaterInstances(selfPID int) []otherMenubarInstance {
	_ = selfPID
	return nil
}

func activateRunningApplication(pid int) {
	_ = pid
}
