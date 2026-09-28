package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

static void hideFromDock(void) {
	[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
}

static void activateApp(void) {
	[NSApp activateIgnoringOtherApps:YES];
}
*/
import "C"

// hideFromDock turns the app into an accessory app, which has no Dock icon
// and no menu bar, as it lives in the system tray. glfw makes the app a
// regular app when it starts, so this must be called after that, on the
// main thread.
func hideFromDock() {
	C.hideFromDock()
}

// activateApp brings the app to the front, an accessory app does not get
// activated when one of its windows is shown.
func activateApp() {
	C.activateApp()
}
