package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lu-zhengda/updater/internal/app"
	"github.com/lu-zhengda/updater/internal/checker"
	"github.com/lu-zhengda/updater/internal/config"
)

type recordingNotifier struct {
	notices []userNotice
	err     error
}

func (r *recordingNotifier) Notify(_ context.Context, notice userNotice) error {
	r.notices = append(r.notices, notice)
	return r.err
}

func useNotifier(t *testing.T, n notifier) {
	t.Helper()
	prev := appNotifier
	appNotifier = n
	t.Cleanup(func() { appNotifier = prev })
}

func TestExecutableInAppBundle(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{path: "/Applications/Updater.app/Contents/MacOS/updater", want: true},
		{path: "Updater.app/Contents/MacOS/updater", want: true},
		{path: "bin/updater", want: false},
		{path: "updater", want: false},
		{path: "", want: false},
	}
	for _, tc := range cases {
		if got := executableInAppBundle(tc.path); got != tc.want {
			t.Errorf("executableInAppBundle(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestUpdateResultNotice(t *testing.T) {
	notice, ok := updateResultNotice([]string{"Firefox", "Chrome"}, []string{"Slack"})
	if !ok {
		t.Fatal("expected a notice")
	}
	if notice.title != "Updater" || notice.subtitle != "" || notice.body != "Updated: Firefox, Chrome. Needs attention: Slack" {
		t.Fatalf("notice = %+v", notice)
	}
	if _, ok := updateResultNotice(nil, nil); ok {
		t.Fatal("empty result posted a notice")
	}
	failedOnly, ok := updateResultNotice(nil, []string{"Slack"})
	if !ok || failedOnly.body != "Needs attention: Slack" {
		t.Fatalf("failed notice = %+v ok=%v", failedOnly, ok)
	}
}

func TestAutoUpdateAfterNotifyUsesNativeNotifier(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	rec := &recordingNotifier{}
	useNotifier(t, rec)
	runner := &yoloRunner{MultiMockCmdRunner: checker.MultiMockCmdRunner{Responses: map[string]checker.MockResponse{}}}
	prevRunner := newRunner
	newRunner = func() checker.CmdRunner { return runner }
	t.Cleanup(func() { newRunner = prevRunner })

	updated, failed := autoUpdateAfterNotify(context.Background(), &config.Config{YOLOMode: true}, []*checker.UpdateResult{{
		App:            &app.App{Name: "Firefox", FormulaName: "firefox"},
		Source:         "formula",
		HasUpdate:      true,
		CurrentVersion: "120.0",
		LatestVersion:  "121.0",
	}})
	if len(updated) != 1 || updated[0] != "Firefox" || len(failed) != 0 {
		t.Fatalf("updated %v failed %v", updated, failed)
	}
	if len(rec.notices) != 1 {
		t.Fatalf("notices = %+v", rec.notices)
	}
	if rec.notices[0].title != "Updater" || rec.notices[0].body != "Updated: Firefox" || rec.notices[0].subtitle != "" {
		t.Fatalf("notice = %+v", rec.notices[0])
	}
	for _, call := range runner.calls {
		if strings.Contains(call, "osascript") || strings.Contains(call, "display notification") {
			t.Fatalf("update flow shelled out to osascript: %v", runner.calls)
		}
	}
}

func TestAutoUpdateAfterNotifySkipsNoticeWhenNothingChanges(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	rec := &recordingNotifier{}
	useNotifier(t, rec)
	prevRunner := newRunner
	newRunner = func() checker.CmdRunner { return &yoloRunner{} }
	t.Cleanup(func() { newRunner = prevRunner })

	updated, failed := autoUpdateAfterNotify(context.Background(), &config.Config{YOLOMode: true}, nil)
	if len(updated)+len(failed) != 0 || len(rec.notices) != 0 {
		t.Fatalf("updated %v failed %v notices %+v", updated, failed, rec.notices)
	}
}

func TestBundleNotifierDropsOutsideBundle(t *testing.T) {
	if defaultNotificationBundleReady() {
		t.Skip("this process is inside an app bundle")
	}
	prevReady, prevDeliver := notificationBundleReady, deliverNotification
	t.Cleanup(func() {
		notificationBundleReady, deliverNotification = prevReady, prevDeliver
	})
	notificationBundleReady = defaultNotificationBundleReady
	called := false
	deliverNotification = func(string, string, string) error {
		called = true
		return nil
	}
	err := (bundleNotifier{}).Notify(context.Background(), userNotice{title: "Updater", body: "Updated: Firefox"})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("native delivery ran outside an app bundle")
	}
}

func TestFocusPIDsSkipsSelfAndJunk(t *testing.T) {
	got := focusPIDs("10\nnot\n0\n-3\n11\n", 10)
	if len(got) != 1 || got[0] != 11 {
		t.Fatalf("pids = %v", got)
	}
	if len(focusPIDs("", 1)) != 0 {
		t.Fatal("empty output focused a process")
	}
}

func TestBundleNotifierReturnsDeliveryFailure(t *testing.T) {
	prevReady, prevDeliver := notificationBundleReady, deliverNotification
	t.Cleanup(func() {
		notificationBundleReady, deliverNotification = prevReady, prevDeliver
	})
	notificationBundleReady = func() bool { return true }
	deliverNotification = func(string, string, string) error {
		return errors.New("notification was not delivered")
	}
	err := (bundleNotifier{}).Notify(context.Background(), userNotice{title: "Updater", body: "Updated: Firefox"})
	if err == nil || !strings.Contains(err.Error(), "notification was not delivered") {
		t.Fatalf("err = %v", err)
	}
}

func TestSendNotificationReturnsDeliveryFailure(t *testing.T) {
	useNotifier(t, &recordingNotifier{err: errors.New("notification was not delivered")})
	err := sendNotification(context.Background(), 1, "Firefox", "")
	if err == nil || !strings.Contains(err.Error(), "failed to send notification") {
		t.Fatalf("err = %v", err)
	}
}

func TestBundleNotifierDeliversWhenBundled(t *testing.T) {
	prevReady, prevDeliver, prevAsk := notificationBundleReady, deliverNotification, askNotificationPermission
	t.Cleanup(func() {
		notificationBundleReady, deliverNotification, askNotificationPermission = prevReady, prevDeliver, prevAsk
	})
	notificationBundleReady = func() bool { return true }
	var got userNotice
	deliverNotification = func(title, subtitle, body string) error {
		got = userNotice{title: title, subtitle: subtitle, body: body}
		return nil
	}
	asked := false
	askNotificationPermission = func() { asked = true }
	notice := userNotice{title: "Updater", subtitle: "1 major update", body: "Firefox"}
	if err := (bundleNotifier{}).Notify(context.Background(), notice); err != nil {
		t.Fatal(err)
	}
	if got != notice {
		t.Fatalf("delivered %+v", got)
	}
	if asked {
		t.Fatal("posting asked for permission")
	}
}

func TestBundleNotifierDropsCancelledContext(t *testing.T) {
	prevReady, prevDeliver := notificationBundleReady, deliverNotification
	t.Cleanup(func() {
		notificationBundleReady, deliverNotification = prevReady, prevDeliver
	})
	notificationBundleReady = func() bool { return true }
	called := false
	deliverNotification = func(string, string, string) error {
		called = true
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (bundleNotifier{}).Notify(ctx, userNotice{title: "Updater", body: "Updated: Firefox"}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("delivered after cancel")
	}
}

func TestNotificationPermissionAskedOnlyForBundle(t *testing.T) {
	prevReady, prevAsk := notificationBundleReady, askNotificationPermission
	t.Cleanup(func() {
		notificationBundleReady, askNotificationPermission = prevReady, prevAsk
	})
	asked := 0
	askNotificationPermission = func() { asked++ }
	notificationBundleReady = func() bool { return false }
	requestNotificationPermission()
	if asked != 0 {
		t.Fatal("asked outside a bundle")
	}
	notificationBundleReady = func() bool { return true }
	requestNotificationPermission()
	if asked != 1 {
		t.Fatalf("asked = %d, want 1", asked)
	}
}
