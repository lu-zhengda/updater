//go:build darwin

package main

// platform: UNUserNotificationCenter aborts unless this process is an .app whose Info.plist sets CFBundleIdentifier.
// platform: an authorization prompt abandoned by process exit is stored as a denial, so only the long-running menu bar app asks.

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework UserNotifications -framework AppKit -framework Foundation
#include <stdlib.h>
#import <objc/runtime.h>
#import <os/lock.h>
#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>
#import <UserNotifications/UserNotifications.h>

extern void updaterFocusFromNotification(void);

static int updaterNotificationBundleReady(void) {
	@autoreleasepool {
		NSBundle *bundle = [NSBundle mainBundle];
		if (bundle == nil) {
			return 0;
		}
		NSString *ident = [bundle bundleIdentifier];
		NSString *path = [bundle bundlePath];
		if (ident == nil || [ident length] == 0 || path == nil) {
			return 0;
		}
		return [[path lowercaseString] hasSuffix:@".app"] ? 1 : 0;
	}
}

static NSString *updaterNotificationString(const char *s) {
	if (s == NULL) {
		return @"";
	}
	NSString *out = [NSString stringWithUTF8String:s];
	if (out == nil) {
		return @"";
	}
	return out;
}

#if OS_OBJECT_USE_OBJC
extern void objc_release(id obj);
#endif

static void updaterReleaseSemaphore(dispatch_semaphore_t sem) {
	if (sem == NULL) {
		return;
	}
#if OS_OBJECT_USE_OBJC
	objc_release((id)sem);
#else
	dispatch_release(sem);
#endif
}

typedef struct updaterNoteWait {
	dispatch_semaphore_t sem;
	os_unfair_lock lock;
	int value;
	int ready;
	int refs;
} updaterNoteWait;

static updaterNoteWait *updaterNoteWaitCreate(void) {
	updaterNoteWait *state = calloc(1, sizeof(*state));
	if (state == NULL) {
		return NULL;
	}
	state->sem = dispatch_semaphore_create(0);
	if (state->sem == NULL) {
		free(state);
		return NULL;
	}
	state->refs = 2;
	return state;
}

static void updaterNoteWaitRelease(updaterNoteWait *state) {
	if (state == NULL) {
		return;
	}
	os_unfair_lock_lock(&state->lock);
	state->refs--;
	int drop = state->refs == 0;
	os_unfair_lock_unlock(&state->lock);
	if (drop) {
		updaterReleaseSemaphore(state->sem);
		free(state);
	}
}

static void updaterWaitSignal(dispatch_semaphore_t sem, double seconds) {
	if (sem == NULL) {
		return;
	}
	if ([NSThread isMainThread]) {
		NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:seconds];
		while (dispatch_semaphore_wait(sem, DISPATCH_TIME_NOW) != 0) {
			if ([deadline timeIntervalSinceNow] <= 0) {
				return;
			}
			[[NSRunLoop currentRunLoop] runMode:NSDefaultRunLoopMode beforeDate:[NSDate dateWithTimeIntervalSinceNow:0.05]];
		}
		return;
	}
	dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, (int64_t)(seconds * NSEC_PER_SEC)));
}

static int updaterNoteWaitFinish(updaterNoteWait *state, double seconds, int *value) {
	updaterWaitSignal(state->sem, seconds);
	os_unfair_lock_lock(&state->lock);
	int ready = state->ready;
	int got = state->value;
	os_unfair_lock_unlock(&state->lock);
	updaterNoteWaitRelease(state);
	if (!ready) {
		return 0;
	}
	if (value != NULL) {
		*value = got;
	}
	return 1;
}

@interface UpdaterNotificationDelegate : NSObject <UNUserNotificationCenterDelegate>
@end

static UpdaterNotificationDelegate *updaterNotificationDelegate;

@implementation UpdaterNotificationDelegate

- (void)userNotificationCenter:(UNUserNotificationCenter *)center
       willPresentNotification:(UNNotification *)notification
         withCompletionHandler:(void (^)(UNNotificationPresentationOptions))completionHandler {
	(void)center;
	(void)notification;
	completionHandler(UNNotificationPresentationOptionBanner | UNNotificationPresentationOptionList | UNNotificationPresentationOptionSound);
}

- (void)userNotificationCenter:(UNUserNotificationCenter *)center
didReceiveNotificationResponse:(UNNotificationResponse *)response
         withCompletionHandler:(void (^)(void))completionHandler {
	(void)center;
	if (response != nil && [response.actionIdentifier isEqualToString:UNNotificationDefaultActionIdentifier]) {
		updaterFocusFromNotification();
	}
	completionHandler();
}

@end

static void updaterInstallNotificationDelegate(UNUserNotificationCenter *center) {
	if (center == nil) {
		return;
	}
	if (updaterNotificationDelegate == nil) {
		updaterNotificationDelegate = [UpdaterNotificationDelegate new];
	}
	if (center.delegate == updaterNotificationDelegate) {
		return;
	}
	if ([NSThread isMainThread]) {
		center.delegate = updaterNotificationDelegate;
		return;
	}
	UpdaterNotificationDelegate *delegate = updaterNotificationDelegate;
	dispatch_async(dispatch_get_main_queue(), ^{
		center.delegate = delegate;
	});
}

static void updaterAskNotificationPermission(void) {
	@autoreleasepool {
		if (!updaterNotificationBundleReady()) {
			return;
		}
		void (^work)(void) = ^{
			@try {
				[NSApplication sharedApplication];
				UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
				updaterInstallNotificationDelegate(center);
				[center getNotificationSettingsWithCompletionHandler:^(UNNotificationSettings *settings) {
					if (settings == nil || settings.authorizationStatus != UNAuthorizationStatusNotDetermined) {
						return;
					}
					dispatch_async(dispatch_get_main_queue(), ^{
						@try {
							UNUserNotificationCenter *again = [UNUserNotificationCenter currentNotificationCenter];
							[again requestAuthorizationWithOptions:(UNAuthorizationOptionAlert | UNAuthorizationOptionSound)
							                      completionHandler:^(BOOL granted, NSError *error) {
								(void)granted;
								(void)error;
							}];
						} @catch (NSException *exception) {
							(void)exception;
						}
					});
				}];
			} @catch (NSException *exception) {
				(void)exception;
			}
		};
		if ([NSThread isMainThread]) {
			work();
			return;
		}
		dispatch_async(dispatch_get_main_queue(), work);
	}
}

static int updaterPostNotification(const char *title, const char *subtitle, const char *body) {
	@autoreleasepool {
		if (!updaterNotificationBundleReady()) {
			return 0;
		}
		UNUserNotificationCenter *center = nil;
		@try {
			if ([NSThread isMainThread]) {
				[NSApplication sharedApplication];
			}
			center = [UNUserNotificationCenter currentNotificationCenter];
		} @catch (NSException *exception) {
			(void)exception;
			return 0;
		}
		if (center == nil) {
			return 0;
		}
		updaterInstallNotificationDelegate(center);

		updaterNoteWait *statusWait = updaterNoteWaitCreate();
		if (statusWait == NULL) {
			return 0;
		}
		[center getNotificationSettingsWithCompletionHandler:^(UNNotificationSettings *settings) {
			os_unfair_lock_lock(&statusWait->lock);
			if (settings != nil) {
				statusWait->value = (int)settings.authorizationStatus;
			}
			statusWait->ready = 1;
			os_unfair_lock_unlock(&statusWait->lock);
			dispatch_semaphore_signal(statusWait->sem);
			updaterNoteWaitRelease(statusWait);
		}];
		int status = 0;
		if (!updaterNoteWaitFinish(statusWait, 3.0, &status)) {
			return 0;
		}
		UNAuthorizationStatus auth = (UNAuthorizationStatus)status;
		if (auth != UNAuthorizationStatusAuthorized && auth != UNAuthorizationStatusProvisional) {
			return 0;
		}

		UNMutableNotificationContent *content = [[[UNMutableNotificationContent alloc] init] autorelease];
		content.title = updaterNotificationString(title);
		content.subtitle = updaterNotificationString(subtitle);
		content.body = updaterNotificationString(body);
		NSString *ident = [[NSUUID UUID] UUIDString];
		UNNotificationRequest *request = [UNNotificationRequest requestWithIdentifier:ident content:content trigger:nil];
		updaterNoteWait *addWait = updaterNoteWaitCreate();
		if (addWait == NULL) {
			return 2;
		}
		[center addNotificationRequest:request withCompletionHandler:^(NSError *error) {
			os_unfair_lock_lock(&addWait->lock);
			addWait->value = error == nil ? 1 : 0;
			addWait->ready = 1;
			os_unfair_lock_unlock(&addWait->lock);
			dispatch_semaphore_signal(addWait->sem);
			updaterNoteWaitRelease(addWait);
		}];
		int posted = 0;
		if (!updaterNoteWaitFinish(addWait, 3.0, &posted) || posted != 1) {
			return 2;
		}
		return 1;
	}
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

func deliverBundleNotification(title, subtitle, body string) error {
	cTitle := C.CString(title)
	defer C.free(unsafe.Pointer(cTitle))
	cSubtitle := C.CString(subtitle)
	defer C.free(unsafe.Pointer(cSubtitle))
	cBody := C.CString(body)
	defer C.free(unsafe.Pointer(cBody))
	if C.updaterPostNotification(cTitle, cSubtitle, cBody) == 2 {
		return errors.New("notification was not delivered")
	}
	return nil
}

func probeNotificationBundle() bool {
	return C.updaterNotificationBundleReady() != 0
}

func askBundleNotificationPermission() {
	C.updaterAskNotificationPermission()
}
