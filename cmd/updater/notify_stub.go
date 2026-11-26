//go:build !darwin

package main

func deliverBundleNotification(title, subtitle, body string) error {
	_, _, _ = title, subtitle, body
	return nil
}

func askBundleNotificationPermission() {}

func probeNotificationBundle() bool {
	return false
}
