//go:build darwin

package main

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation

#include <CoreGraphics/CoreGraphics.h>

// グローバル状態（Cコールバックから参照）
static int g_grabbed;
static int g_warpX, g_warpY; // grabbed 時のカーソル固定位置

// イベントバッファ（GoとCの橋渡し）
#define MAX_EVENTS 256
typedef struct {
	int type;   // 0=move, 1=buttonDown, 2=buttonUp, 3=scroll
	int x, y;
	int dx, dy;
	int button; // 0=left, 1=right, 2=middle
	int scrollX, scrollY;
} MouseEvt;

static MouseEvt g_evtBuf[MAX_EVENTS];
static int g_evtCount;
static CGFloat g_lastX, g_lastY; // 前回座標（相対移動計算用）
static int g_hasPrev;

static CGEventRef mouseCallback(CGEventTapProxy proxy, CGEventType type,
                                 CGEventRef event, void *refcon) {
	(void)proxy; (void)refcon;

	if (g_evtCount >= MAX_EVENTS) return event;

	CGPoint loc = CGEventGetLocation(event);
	MouseEvt *e = &g_evtBuf[g_evtCount];

	int dx = 0, dy = 0;
	if (g_hasPrev) {
		dx = (int)(loc.x - g_lastX);
		dy = (int)(loc.y - g_lastY);
	}
	g_lastX = loc.x;
	g_lastY = loc.y;
	g_hasPrev = 1;

	e->x = (int)loc.x;
	e->y = (int)loc.y;
	e->dx = dx;
	e->dy = dy;
	e->button = 0;
	e->scrollX = 0;
	e->scrollY = 0;

	switch (type) {
	case kCGEventMouseMoved:
	case kCGEventLeftMouseDragged:
	case kCGEventRightMouseDragged:
	case kCGEventOtherMouseDragged:
		e->type = 0; // move
		break;
	case kCGEventLeftMouseDown:
		e->type = 1; e->button = 0;
		break;
	case kCGEventLeftMouseUp:
		e->type = 2; e->button = 0;
		break;
	case kCGEventRightMouseDown:
		e->type = 1; e->button = 1;
		break;
	case kCGEventRightMouseUp:
		e->type = 2; e->button = 1;
		break;
	case kCGEventOtherMouseDown:
		e->type = 1; e->button = 2;
		break;
	case kCGEventOtherMouseUp:
		e->type = 2; e->button = 2;
		break;
	case kCGEventScrollWheel:
		e->type = 3;
		e->scrollX = (int)CGEventGetIntegerValueField(event, kCGScrollWheelEventDeltaAxis2);
		e->scrollY = (int)CGEventGetIntegerValueField(event, kCGScrollWheelEventDeltaAxis1);
		break;
	default:
		return event;
	}
	g_evtCount++;

	// grabbed 状態ならカーソルを固定位置にワープして入力を消費
	if (g_grabbed) {
		CGWarpMouseCursorPosition(CGPointMake(g_warpX, g_warpY));
		return NULL; // イベントを消費
	}

	return event;
}

static CFMachPortRef g_tap;
static CFRunLoopSourceRef g_src;
static CFRunLoopRef g_runLoop;

static int startMouseTap(void) {
	CGEventMask mask =
		CGEventMaskBit(kCGEventMouseMoved) |
		CGEventMaskBit(kCGEventLeftMouseDown) |
		CGEventMaskBit(kCGEventLeftMouseUp) |
		CGEventMaskBit(kCGEventRightMouseDown) |
		CGEventMaskBit(kCGEventRightMouseUp) |
		CGEventMaskBit(kCGEventOtherMouseDown) |
		CGEventMaskBit(kCGEventOtherMouseUp) |
		CGEventMaskBit(kCGEventLeftMouseDragged) |
		CGEventMaskBit(kCGEventRightMouseDragged) |
		CGEventMaskBit(kCGEventOtherMouseDragged) |
		CGEventMaskBit(kCGEventScrollWheel);

	g_tap = CGEventTapCreate(kCGHIDEventTap, kCGHeadInsertEventTap,
	                          kCGEventTapOptionDefault, mask,
	                          mouseCallback, NULL);
	if (!g_tap) return -1;

	g_src = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, g_tap, 0);
	g_runLoop = CFRunLoopGetCurrent();
	CFRunLoopAddSource(g_runLoop, g_src, kCFRunLoopCommonModes);
	CGEventTapEnable(g_tap, true);

	g_evtCount = 0;
	g_grabbed = 0;
	g_hasPrev = 0;

	CFRunLoopRun();
	return 0;
}

static void stopMouseTap(void) {
	if (g_runLoop) {
		CFRunLoopStop(g_runLoop);
	}
	if (g_tap) {
		CGEventTapEnable(g_tap, false);
		CFRelease(g_tap);
		g_tap = NULL;
	}
	if (g_src) {
		CFRelease(g_src);
		g_src = NULL;
	}
	g_runLoop = NULL;
}

static void setGrabbed(int grabbed, int warpX, int warpY) {
	g_grabbed = grabbed;
	g_warpX = warpX;
	g_warpY = warpY;
}

static int drainEvents(MouseEvt *out, int maxOut) {
	int n = g_evtCount;
	if (n > maxOut) n = maxOut;
	for (int i = 0; i < n; i++) {
		out[i] = g_evtBuf[i];
	}
	// 残りを先頭にシフト
	int remaining = g_evtCount - n;
	for (int i = 0; i < remaining; i++) {
		g_evtBuf[i] = g_evtBuf[n + i];
	}
	g_evtCount = remaining;
	return n;
}

static void getMainScreenSize(int *w, int *h) {
	CGDirectDisplayID mainDisplay = CGMainDisplayID();
	*w = (int)CGDisplayPixelsWide(mainDisplay);
	*h = (int)CGDisplayPixelsHigh(mainDisplay);
}

static void warpCursor(int x, int y) {
	CGWarpMouseCursorPosition(CGPointMake(x, y));
}
*/
import "C"
import (
	"fmt"
	"time"
)

type darwinMouseHook struct {
	callback func(MouseHookEvent)
	stopCh   chan struct{}
	running  bool
}

func newMouseHook() MouseHook {
	return &darwinMouseHook{}
}

func (h *darwinMouseHook) Start(callback func(MouseHookEvent)) error {
	h.callback = callback
	h.stopCh = make(chan struct{})
	h.running = true

	// CGEventTap は CFRunLoop 上で動くので別 goroutine で開始
	errCh := make(chan error, 1)
	go func() {
		ret := C.startMouseTap()
		if ret != 0 {
			errCh <- fmt.Errorf("CGEventTapCreate failed (ret=%d). Accessibility permission required", int(ret))
		}
		close(errCh)
	}()

	// イベントポーリング goroutine
	go func() {
		var buf [64]C.MouseEvt
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-h.stopCh:
				return
			case <-ticker.C:
				n := int(C.drainEvents(&buf[0], C.int(len(buf))))
				for i := 0; i < n; i++ {
					e := buf[i]
					evt := MouseHookEvent{
						X:       int(e.x),
						Y:       int(e.y),
						DX:      int(e.dx),
						DY:      int(e.dy),
						Button:  int(e.button),
						ScrollX: int(e.scrollX),
						ScrollY: int(e.scrollY),
					}
					switch int(e._type) {
					case 0:
						evt.Type = MouseHookMove
					case 1:
						evt.Type = MouseHookButtonDown
					case 2:
						evt.Type = MouseHookButtonUp
					case 3:
						evt.Type = MouseHookScroll
					}
					h.callback(evt)
				}
			}
		}
	}()

	// 起動エラーがあれば短時間で返る
	select {
	case err := <-errCh:
		if err != nil {
			h.running = false
			return err
		}
	case <-time.After(100 * time.Millisecond):
		// 起動成功（CFRunLoopRun がブロック中）
	}
	return nil
}

func (h *darwinMouseHook) Stop() {
	if !h.running {
		return
	}
	h.running = false
	close(h.stopCh)
	C.stopMouseTap()
}

func (h *darwinMouseHook) SetGrabbed(grabbed bool) {
	var g C.int
	if grabbed {
		g = 1
	}
	// grabbed 時はカーソルを現在位置に固定
	var w, hh C.int
	C.getMainScreenSize(&w, &hh)
	// 現在の画面端の座標に固定（エッジに応じて呼び出し元が WarpCursor で設定）
	C.setGrabbed(g, w/2, hh/2)
}

func (h *darwinMouseHook) GetScreenSize() (width, height int) {
	var w, hh C.int
	C.getMainScreenSize(&w, &hh)
	return int(w), int(hh)
}

func (h *darwinMouseHook) WarpCursor(x, y int) {
	C.warpCursor(C.int(x), C.int(y))
}
