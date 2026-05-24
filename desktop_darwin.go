package main

/*
#cgo darwin CFLAGS: -x objective-c -fobjc-arc
#cgo darwin LDFLAGS: -framework Cocoa -framework WebKit

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#include <stdlib.h>

@interface ManagerWindowDelegate : NSObject <NSWindowDelegate>
@end

@implementation ManagerWindowDelegate
- (void)windowWillClose:(NSNotification *)notification {
	[NSApp terminate:nil];
}
@end

static ManagerWindowDelegate *managerWindowDelegate;

static void runManagerWindow(const char *titleChars, const char *urlChars) {
	@autoreleasepool {
		NSString *title = [NSString stringWithUTF8String:titleChars];
		NSString *urlString = [NSString stringWithUTF8String:urlChars];
		NSApplication *app = [NSApplication sharedApplication];
		[app setActivationPolicy:NSApplicationActivationPolicyRegular];

		NSRect frame = NSMakeRect(0, 0, 1180, 780);
		NSWindowStyleMask style = NSWindowStyleMaskTitled |
			NSWindowStyleMaskClosable |
			NSWindowStyleMaskMiniaturizable |
			NSWindowStyleMaskResizable;
		NSWindow *window = [[NSWindow alloc] initWithContentRect:frame
			styleMask:style
			backing:NSBackingStoreBuffered
			defer:NO];
		[window setTitle:title];
		[window center];
		[window setReleasedWhenClosed:NO];
		managerWindowDelegate = [[ManagerWindowDelegate alloc] init];
		[window setDelegate:managerWindowDelegate];

		WKWebViewConfiguration *configuration = [[WKWebViewConfiguration alloc] init];
		WKWebView *webView = [[WKWebView alloc] initWithFrame:frame configuration:configuration];
		[webView setAutoresizingMask:NSViewWidthSizable | NSViewHeightSizable];
		[[window contentView] addSubview:webView];
		[webView loadRequest:[NSURLRequest requestWithURL:[NSURL URLWithString:urlString]]];

		[window makeKeyAndOrderFront:nil];
		[app activateIgnoringOtherApps:YES];
		[app run];
	}
}
*/
import "C"

import (
	"runtime"
	"unsafe"
)

func defaultManagerDesktop() bool {
	return true
}

func lockManagerDesktopThread() {
	runtime.LockOSThread()
}

func runManagerDesktopWindow(title, url string) error {
	cTitle := C.CString(title)
	defer C.free(unsafe.Pointer(cTitle))
	cURL := C.CString(url)
	defer C.free(unsafe.Pointer(cURL))
	C.runManagerWindow(cTitle, cURL)
	return nil
}
