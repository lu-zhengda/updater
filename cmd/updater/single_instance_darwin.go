//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit -framework Foundation
#include <stdlib.h>
#import <AppKit/AppKit.h>
#import <Foundation/Foundation.h>

// updaterListOtherInstances fills outPIDs/outAccessory with peers that share
// bundleID and are not selfPID. Returns the number of peers written (capped
// by cap). Accessory is 1 when activationPolicy is Accessory (menu bar).
static int updaterListOtherInstances(const char *bundleID, int selfPID, int *outPIDs, int *outAccessory, int cap) {
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
*/
import "C"

import (
	"unsafe"

	"github.com/lu-zhengda/updater/internal/app"
)

const maxOtherInstances = 16

// listOtherUpdaterInstances returns other processes with this app's bundle id.
func listOtherUpdaterInstances(selfPID int) []otherMenubarInstance {
	pids := make([]C.int, maxOtherInstances)
	acc := make([]C.int, maxOtherInstances)
	bid := C.CString(app.UpdaterBundleID)
	defer C.free(unsafe.Pointer(bid))
	n := int(C.updaterListOtherInstances(bid, C.int(selfPID), &pids[0], &acc[0], C.int(maxOtherInstances)))
	if n <= 0 {
		return nil
	}
	out := make([]otherMenubarInstance, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, otherMenubarInstance{
			PID:            int(pids[i]),
			BundleID:       app.UpdaterBundleID,
			IsAccessory:    acc[i] != 0,
			FromSameBundle: true,
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
