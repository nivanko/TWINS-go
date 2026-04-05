package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#include <dispatch/dispatch.h>
#import <Cocoa/Cocoa.h>

extern void goSystrayStartCallback(void);

static void systrayStartWrapper(void *ctx) {
	// Save Wails's NSApplication delegate and Dock icon before energye/systray
	// replaces them. systray's nativeStart() calls [NSApp setDelegate:owner]
	// which overwrites Wails's delegate, breaking the Dock icon.
	id savedDelegate = [[NSApplication sharedApplication] delegate];
	NSImage *savedIcon = [[NSApplication sharedApplication] applicationIconImage];

	goSystrayStartCallback();

	// Restore Wails's delegate. systray's internal operations use its own
	// 'owner' variable directly (not [NSApp delegate]), so this is safe.
	if (savedDelegate != nil) {
		[[NSApplication sharedApplication] setDelegate:savedDelegate];
	}
	// Restore the Dock icon in case it was reset during delegate swap.
	if (savedIcon != nil) {
		[[NSApplication sharedApplication] setApplicationIconImage:savedIcon];
	}
}

static void dispatchToMainQueue(void) {
	dispatch_async_f(dispatch_get_main_queue(), NULL, systrayStartWrapper);
}

// setActivationPolicyAccessory hides the Dock icon and menu bar by switching
// the application to an "accessory" app (background agent with status item).
// Dispatched to the main thread because AppKit requires UI operations there.
static void setActivationPolicyAccessory(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		[[NSApplication sharedApplication] setActivationPolicy:NSApplicationActivationPolicyAccessory];
	});
}

// setActivationPolicyRegular restores the Dock icon and menu bar, then
// activates the app so it comes to the foreground.
// Dispatched to the main thread because AppKit requires UI operations there.
static void setActivationPolicyRegular(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		[[NSApplication sharedApplication] setActivationPolicy:NSApplicationActivationPolicyRegular];
		[[NSApplication sharedApplication] activateIgnoringOtherApps:YES];
	});
}
*/
import "C"

var pendingSystrayStart func()

//export goSystrayStartCallback
func goSystrayStartCallback() {
	if pendingSystrayStart != nil {
		fn := pendingSystrayStart
		pendingSystrayStart = nil
		fn()
	}
}

// dispatchSystrayToMainThread dispatches the systray start function to the
// macOS main thread via dispatch_async. This is required because NSStatusItem
// (and its NSStatusBarWindow) must be created on the main thread.
func dispatchSystrayToMainThread(startFn func()) {
	pendingSystrayStart = startFn
	C.dispatchToMainQueue()
}

// hideDockIcon switches NSApplication to accessory mode, hiding the Dock icon
// and menu bar. The app remains running with only its status bar (tray) item.
func hideDockIcon() {
	C.setActivationPolicyAccessory()
}

// showDockIcon restores NSApplication to regular mode, showing the Dock icon
// and menu bar, and activates the app to bring it to the foreground.
func showDockIcon() {
	C.setActivationPolicyRegular()
}

// shouldStartTray returns true on macOS where tray is supported.
func shouldStartTray() bool { return true }

// splashHeightExtra returns 0 on macOS — Wails uses content dimensions.
func splashHeightExtra() int { return 0 }

// setTaskbarIcon is a no-op on macOS — icon handled by the app bundle.
func setTaskbarIcon() {}

// configureWindowsTaskbarIdentity is a no-op on macOS.
func configureWindowsTaskbarIdentity() {}
