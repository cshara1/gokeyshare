//go:build darwin

package main

/*
#cgo LDFLAGS: -framework CoreGraphics
#include <CoreGraphics/CoreGraphics.h>

static void tapKeyCode(CGKeyCode code) {
	CGEventRef down = CGEventCreateKeyboardEvent(NULL, code, true);
	CGEventRef up   = CGEventCreateKeyboardEvent(NULL, code, false);
	CGEventPost(kCGHIDEventTap, down);
	CGEventPost(kCGHIDEventTap, up);
	CFRelease(down);
	CFRelease(up);
}
*/
import "C"
import "log"

var platformKeyMap = map[string]C.CGKeyCode{
	"fn":       0x3F, // kVK_Function
	"eisu":     0x66, // kVK_JIS_Eisu
	"kana_mac": 0x68, // kVK_JIS_Kana
}

func platformKeyTap(key string) bool {
	code, ok := platformKeyMap[key]
	if !ok {
		return false
	}
	C.tapKeyCode(code)
	log.Printf("  → プラットフォーム固有キー送信: %q (keycode=0x%02X)", key, int(code))
	return true
}
