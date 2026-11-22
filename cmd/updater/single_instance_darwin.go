//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit -framework Foundation
#include <stdlib.h>
#include <string.h>
#import <AppKit/AppKit.h>
#import <Foundation/Foundation.h>

// updaterListOtherInstances fills outPIDs/outAccessory/outPaths with peers that
// share bundleID and are not selfPID. Paths are executableURL paths (may be
// empty). Returns the number of peers written (capped by cap). Accessory is 1
// when activationPolicy is Accessory (menu bar).
static int updaterListOtherInstances(const char *bundleID, int selfPID, int *outPIDs, int *outAccessory, char **outPaths, int cap) {
	@autoreleasepool {
		NSString *bid = [NSString stringWithUTF8String:bundleID];
		if (bid == nil || cap <= 0) {
			return 0;
		}
		NSArray<NSRunningApplication *> *apps =
			[NSRunningApplication runningApplicationsWithBundleIdentifier:bid];
		int n = 0;
		for (NSRunningApplication *app in apps) {
			pid_t pid = app.processIdentifier;
			if (pid == selfPID) {
				continue;
			}
			if (n >= cap) {
				break;
			}
			outPIDs[n] = (int)pid;
			outAccessory[n] = (app.activationPolicy == NSApplicationActivationPolicyAccessory) ? 1 : 0;
			NSURL *exe = app.executableURL;
			if (exe != nil && exe.path != nil) {
				outPaths[n] = strdup(exe.path.UTF8String);
			} else {
				outPaths[n] = NULL;
			}
			n++;
		}
		return n;
	}
}

static void updaterActivatePID(int pid) {
	@autoreleasepool {
		NSRunningApplication *app =
			[NSRunningApplication runningApplicationWithProcessIdentifier:(pid_t)pid];
		if (app == nil) {
			return;
		}
		[app activateWithOptions:NSApplicationActivateAllWindows];
	}
}

// updaterTerminatePID asks the peer to quit. force nonzero uses forceTerminate.
// Returns 1 when the app object was missing (already gone) or the call reported
// success; 0 when the terminate request was refused.
static int updaterTerminatePID(int pid, int force) {
	@autoreleasepool {
		NSRunningApplication *app =
			[NSRunningApplication runningApplicationWithProcessIdentifier:(pid_t)pid];
		if (app == nil) {
			return 1;
		}
		BOOL ok;
		if (force) {
			ok = [app forceTerminate];
		} else {
			ok = [app terminate];
		}
		return ok ? 1 : 0;
	}
}

static int updaterPIDAlive(int pid) {
	@autoreleasepool {
		NSRunningApplication *app =
			[NSRunningApplication runningApplicationWithProcessIdentifier:(pid_t)pid];
		return app == nil ? 0 : 1;
	}
}
*/
import "C"

import (
	"time"
	"unsafe"

	"github.com/lu-zhengda/updater/internal/app"
)

const maxOtherInstances = 16

// listOtherUpdaterInstances returns other processes with this app's bundle id.
func listOtherUpdaterInstances(selfPID int) []otherMenubarInstance {
	pids := make([]C.int, maxOtherInstances)
	acc := make([]C.int, maxOtherInstances)
	paths := make([]*C.char, maxOtherInstances)
	bid := C.CString(app.UpdaterBundleID)
	defer C.free(unsafe.Pointer(bid))
	n := int(C.updaterListOtherInstances(bid, C.int(selfPID), &pids[0], &acc[0], &paths[0], C.int(maxOtherInstances)))
	if n <= 0 {
		return nil
	}
	out := make([]otherMenubarInstance, 0, n)
	for i := 0; i < n; i++ {
		path := ""
		if paths[i] != nil {
			path = C.GoString(paths[i])
			C.free(unsafe.Pointer(paths[i]))
		}
		out = append(out, otherMenubarInstance{
			PID:         int(pids[i]),
			BundleID:    app.UpdaterBundleID,
			Path:        path,
			IsAccessory: acc[i] != 0,
		})
	}
	return out
}

func activateRunningApplication(pid int) {
	if pid <= 0 {
		return
	}
	C.updaterActivatePID(C.int(pid))
}

func terminateMenubarInstance(pid int, timeout time.Duration) {
	if pid <= 0 {
		return
	}
	C.updaterTerminatePID(C.int(pid), 0)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if C.updaterPIDAlive(C.int(pid)) == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	C.updaterTerminatePID(C.int(pid), 1)
}
