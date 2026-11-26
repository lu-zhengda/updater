//go:build darwin

package main

import "C"

//export updaterFocusFromNotification
func updaterFocusFromNotification() {
	go openUpdaterWindow()
}
