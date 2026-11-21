package main

import (
	"os"
	"syscall"
)

// menubarLockFile stays open for the process lifetime so the flock is held
// until exit. A crash releases it for the next start.
var menubarLockFile *os.File

// tryAcquireMenubarLock takes an exclusive non-blocking flock on the per-user
// menubar lock. held is false when another process already owns it.
func tryAcquireMenubarLock() (held bool, err error) {
	path, err := menubarLockPath()
	if err != nil {
		return false, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, err
	}
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		_ = f.Close()
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return false, nil
		}
		return false, err
	}
	menubarLockFile = f
	return true, nil
}
