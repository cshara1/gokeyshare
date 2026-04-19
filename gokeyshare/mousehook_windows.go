//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	user32              = syscall.NewLazyDLL("user32.dll")
	procSetWindowsHookEx   = user32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx     = user32.NewProc("CallNextHookEx")
	procGetMessage         = user32.NewProc("GetMessageW")
	procGetSystemMetrics   = user32.NewProc("GetSystemMetrics")
	procSetCursorPos       = user32.NewProc("SetCursorPos")
	procGetCursorPos       = user32.NewProc("GetCursorPos")
)

const (
	whMouseLL    = 14 // WH_MOUSE_LL
	smCXScreen   = 0  // SM_CXSCREEN
	smCYScreen   = 1  // SM_CYSCREEN
	wmMouseMove  = 0x0200
	wmLButtonDown = 0x0201
	wmLButtonUp   = 0x0202
	wmRButtonDown = 0x0204
	wmRButtonUp   = 0x0205
	wmMButtonDown = 0x0207
	wmMButtonUp   = 0x0208
	wmMouseWheel  = 0x020A
	wmMouseHWheel = 0x020E
)

type point struct {
	X, Y int32
}

type msllHookStruct struct {
	Pt        point
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type windowsMouseHook struct {
	callback func(MouseHookEvent)
	hook     uintptr
	grabbed  bool
	warpX    int
	warpY    int
	prevX    int
	prevY    int
	hasPrev  bool
}

// グローバル参照（コールバックから使用）
var gWinHook *windowsMouseHook

func newMouseHook() MouseHook {
	h := &windowsMouseHook{}
	gWinHook = h
	return h
}

func lowLevelMouseProc(nCode int, wParam uintptr, lParam uintptr) uintptr {
	if nCode >= 0 && gWinHook != nil && gWinHook.callback != nil {
		ms := (*msllHookStruct)(unsafe.Pointer(lParam))
		x := int(ms.Pt.X)
		y := int(ms.Pt.Y)

		dx, dy := 0, 0
		if gWinHook.hasPrev {
			dx = x - gWinHook.prevX
			dy = y - gWinHook.prevY
		}
		gWinHook.prevX = x
		gWinHook.prevY = y
		gWinHook.hasPrev = true

		evt := MouseHookEvent{X: x, Y: y, DX: dx, DY: dy}

		switch wParam {
		case wmMouseMove:
			evt.Type = MouseHookMove
		case wmLButtonDown:
			evt.Type = MouseHookButtonDown
			evt.Button = 0
		case wmLButtonUp:
			evt.Type = MouseHookButtonUp
			evt.Button = 0
		case wmRButtonDown:
			evt.Type = MouseHookButtonDown
			evt.Button = 1
		case wmRButtonUp:
			evt.Type = MouseHookButtonUp
			evt.Button = 1
		case wmMButtonDown:
			evt.Type = MouseHookButtonDown
			evt.Button = 2
		case wmMButtonUp:
			evt.Type = MouseHookButtonUp
			evt.Button = 2
		case wmMouseWheel:
			evt.Type = MouseHookScroll
			// HIWORD of mouseData is the wheel delta
			delta := int16(ms.MouseData >> 16)
			evt.ScrollY = int(delta) / 120
		case wmMouseHWheel:
			evt.Type = MouseHookScroll
			delta := int16(ms.MouseData >> 16)
			evt.ScrollX = int(delta) / 120
		default:
			goto next
		}

		gWinHook.callback(evt)

		if gWinHook.grabbed {
			procSetCursorPos.Call(uintptr(gWinHook.warpX), uintptr(gWinHook.warpY))
			return 1 // イベントを消費
		}
	}

next:
	ret, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
	return ret
}

func (h *windowsMouseHook) Start(callback func(MouseHookEvent)) error {
	h.callback = callback

	hookProc := syscall.NewCallback(lowLevelMouseProc)
	ret, _, err := procSetWindowsHookEx.Call(whMouseLL, hookProc, 0, 0)
	if ret == 0 {
		return fmt.Errorf("SetWindowsHookEx failed: %v", err)
	}
	h.hook = ret

	// メッセージループ（フックにはメッセージポンプが必要）
	go func() {
		var msg struct {
			Hwnd    uintptr
			Message uint32
			WParam  uintptr
			LParam  uintptr
			Time    uint32
			Pt      point
		}
		for {
			ret, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if ret == 0 { // WM_QUIT
				break
			}
		}
	}()

	return nil
}

func (h *windowsMouseHook) Stop() {
	if h.hook != 0 {
		procUnhookWindowsHookEx.Call(h.hook)
		h.hook = 0
	}
}

func (h *windowsMouseHook) SetGrabbed(grabbed bool) {
	h.grabbed = grabbed
	if grabbed {
		w, hh := h.GetScreenSize()
		h.warpX = w / 2
		h.warpY = hh / 2
	}
}

func (h *windowsMouseHook) GetScreenSize() (width, height int) {
	w, _, _ := procGetSystemMetrics.Call(smCXScreen)
	hh, _, _ := procGetSystemMetrics.Call(smCYScreen)
	return int(w), int(hh)
}

func (h *windowsMouseHook) WarpCursor(x, y int) {
	procSetCursorPos.Call(uintptr(x), uintptr(y))
	h.warpX = x
	h.warpY = y
}
