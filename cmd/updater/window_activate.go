package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

// The window process runs from an LSUIElement bundle, which macOS does not
// activate on launch — the window would order behind other apps while still
// grabbing key focus. Promote to a regular app and activate explicitly
// (both the modern NSRunningApplication path and the legacy NSApp one, for
// pre/post-Sonoma cooperative-activation behavior).
static void updaterActivateApp(void) {
	[NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
	[[NSRunningApplication currentApplication]
		activateWithOptions:(NSApplicationActivateAllWindows | NSApplicationActivateIgnoringOtherApps)];
	[NSApp activateIgnoringOtherApps:YES];
}

static void updaterRaiseWindow(void *win) {
	NSWindow *w = (__bridge NSWindow *)win;
	[w center];
	[w makeKeyAndOrderFront:nil];
}
*/
import "C"

import "unsafe"

// activateWindow makes the updates window a properly activated, frontmost
// window. Must be called on the main thread with the webview window handle.
func activateWindow(win unsafe.Pointer) {
	C.updaterActivateApp()
	C.updaterRaiseWindow(win)
}
