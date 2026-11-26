package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

type userNotice struct {
	title    string
	subtitle string
	body     string
}

type notifier interface {
	Notify(ctx context.Context, notice userNotice) error
}

type bundleNotifier struct{}

func (bundleNotifier) Notify(ctx context.Context, notice userNotice) error {
	if ctx != nil && ctx.Err() != nil {
		return nil
	}
	if notificationBundleReady == nil || !notificationBundleReady() {
		return nil
	}
	if deliverNotification == nil {
		return nil
	}
	return deliverNotification(notice.title, notice.subtitle, notice.body)
}

var appNotifier notifier = bundleNotifier{}

var (
	deliverNotification       = deliverBundleNotification
	askNotificationPermission = askBundleNotificationPermission
	notificationBundleReady   = defaultNotificationBundleReady
)

func notifyUser(ctx context.Context, title, subtitle, body string) {
	if appNotifier == nil {
		return
	}
	_ = appNotifier.Notify(ctx, userNotice{title: title, subtitle: subtitle, body: body})
}

func requestNotificationPermission() {
	if notificationBundleReady == nil || !notificationBundleReady() {
		return
	}
	if askNotificationPermission == nil {
		return
	}
	askNotificationPermission()
}

func defaultNotificationBundleReady() bool {
	if probeNotificationBundle() || runningFromAppBundle() {
		return true
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return false
	}
	return executableInAppBundle(resolved)
}

func executableInAppBundle(path string) bool {
	return strings.Contains(path, ".app/Contents/MacOS/")
}

func updateResultNotice(updated, failed []string) (userNotice, bool) {
	if len(updated)+len(failed) == 0 {
		return userNotice{}, false
	}
	var parts []string
	if len(updated) > 0 {
		parts = append(parts, "Updated: "+strings.Join(updated, ", "))
	}
	if len(failed) > 0 {
		parts = append(parts, "Needs attention: "+strings.Join(failed, ", "))
	}
	return userNotice{title: "Updater", body: strings.Join(parts, ". ")}, true
}
