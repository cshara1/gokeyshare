//go:build linux

package main

/*
#cgo LDFLAGS: -lX11 -lXtst
#include <X11/Xlib.h>
#include <X11/extensions/XTest.h>
#include <X11/keysym.h>

static int tapKeySym(unsigned long keysym) {
	Display *dpy = XOpenDisplay(NULL);
	if (!dpy) return 0;

	KeyCode code = XKeysymToKeycode(dpy, keysym);
	if (code == 0) {
		XCloseDisplay(dpy);
		return 0;
	}

	XTestFakeKeyEvent(dpy, code, True, 0);
	XTestFakeKeyEvent(dpy, code, False, 0);
	XFlush(dpy);
	XCloseDisplay(dpy);
	return 1;
}
*/
import "C"
import "log"

var platformKeyMap = map[string]C.ulong{
	"muhenkan": 0xff22, // XK_Muhenkan
	"henkan":   0xff23, // XK_Henkan_Mode
	"kana":     0xff27, // XK_Hiragana_Katakana
	"eisu":     0xff30, // XK_Eisu_toggle
	"kana_mac": 0xff27, // XK_Hiragana_Katakana (same as kana)
	"hankaku":  0xff2a, // XK_Zenkaku_Hankaku
}

func platformKeyTap(key string) bool {
	sym, ok := platformKeyMap[key]
	if !ok {
		return false
	}
	if C.tapKeySym(sym) == 0 {
		log.Printf("  → キー送信失敗: %q (X11 Display を開けないか keysym=0x%04X のマッピングなし)", key, int(sym))
		return false
	}
	log.Printf("  → プラットフォーム固有キー送信: %q (keysym=0x%04X)", key, int(sym))
	return true
}
