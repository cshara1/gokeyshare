//go:build windows

package main

import (
	"log"
	"syscall"
)

var (
	procKeybdEvent = syscall.NewLazyDLL("user32.dll").NewProc("keybd_event")
)

const keyEventFKeyUp = 0x0002

var platformKeyMap = map[string]uint8{
	"muhenkan": 0x1D, // VK_NONCONVERT
	"henkan":   0x1C, // VK_CONVERT
	"kana":     0x15, // VK_KANA
}

func platformKeyTap(key string) bool {
	vk, ok := platformKeyMap[key]
	if !ok {
		return false
	}
	procKeybdEvent.Call(uintptr(vk), 0, 0, 0)              // key down
	procKeybdEvent.Call(uintptr(vk), 0, keyEventFKeyUp, 0) // key up
	log.Printf("  → プラットフォーム固有キー送信: %q (VK=0x%02X)", key, vk)
	return true
}
