//go:build linux

package main

/*
#cgo LDFLAGS: -lX11 -lXtst -lXi

#include <X11/Xlib.h>
#include <X11/extensions/XTest.h>
#include <X11/extensions/XInput2.h>
#include <string.h>
#include <stdlib.h>

// イベントバッファ
#define MAX_EVENTS 256
typedef struct {
	int type;   // 0=move, 1=buttonDown, 2=buttonUp, 3=scroll
	int x, y;
	int dx, dy;
	int button;
	int scrollX, scrollY;
} MouseEvt;

static Display *g_dpy;
static int g_xi2Opcode;
static int g_running;
static MouseEvt g_evtBuf[MAX_EVENTS];
static int g_evtCount;
static int g_grabbed;

static int initXI2(void) {
	g_dpy = XOpenDisplay(NULL);
	if (!g_dpy) return -1;

	int event, error;
	if (!XQueryExtension(g_dpy, "XInputExtension", &g_xi2Opcode, &event, &error)) {
		XCloseDisplay(g_dpy);
		g_dpy = NULL;
		return -2;
	}

	// XI2 バージョン確認
	int major = 2, minor = 0;
	if (XIQueryVersion(g_dpy, &major, &minor) == BadRequest) {
		XCloseDisplay(g_dpy);
		g_dpy = NULL;
		return -3;
	}

	// 全マスターデバイスからのイベントを監視
	XIEventMask mask;
	unsigned char maskBits[(XI_LASTEVENT + 7) / 8];
	memset(maskBits, 0, sizeof(maskBits));

	XISetMask(maskBits, XI_RawMotion);
	XISetMask(maskBits, XI_RawButtonPress);
	XISetMask(maskBits, XI_RawButtonRelease);

	mask.deviceid = XIAllMasterDevices;
	mask.mask_len = sizeof(maskBits);
	mask.mask = maskBits;

	XISelectEvents(g_dpy, DefaultRootWindow(g_dpy), &mask, 1);
	XFlush(g_dpy);

	g_running = 1;
	g_evtCount = 0;
	g_grabbed = 0;
	return 0;
}

static void pollEvents(void) {
	if (!g_dpy || !g_running) return;

	while (XPending(g_dpy) > 0 && g_evtCount < MAX_EVENTS) {
		XEvent ev;
		XNextEvent(g_dpy, &ev);

		if (ev.xcookie.type != GenericEvent || ev.xcookie.extension != g_xi2Opcode)
			continue;
		if (!XGetEventData(g_dpy, &ev.xcookie))
			continue;

		XIRawEvent *raw = (XIRawEvent *)ev.xcookie.data;
		MouseEvt *me = &g_evtBuf[g_evtCount];
		me->scrollX = 0;
		me->scrollY = 0;

		// rawイベントは画面座標を直接持たないため、現在のカーソル位置を取得
		Window root, child;
		int rootX, rootY, winX, winY;
		unsigned int maskRet;
		XQueryPointer(g_dpy, DefaultRootWindow(g_dpy),
		              &root, &child, &rootX, &rootY, &winX, &winY, &maskRet);
		me->x = rootX;
		me->y = rootY;

		double *vals = raw->raw_values;
		int nvals = raw->valuators.mask_len * 8;
		// raw_values の dx, dy を取得
		int dxSet = 0, dySet = 0;
		double dxVal = 0, dyVal = 0;
		int vi = 0;
		for (int i = 0; i < nvals && i < 2; i++) {
			if (XIMaskIsSet(raw->valuators.mask, i)) {
				if (i == 0) { dxVal = vals[vi]; dxSet = 1; }
				if (i == 1) { dyVal = vals[vi]; dySet = 1; }
				vi++;
			}
		}
		me->dx = dxSet ? (int)dxVal : 0;
		me->dy = dySet ? (int)dyVal : 0;

		switch (ev.xcookie.evtype) {
		case XI_RawMotion:
			me->type = 0;
			me->button = 0;
			g_evtCount++;
			break;
		case XI_RawButtonPress:
			// ボタン 4,5,6,7 はスクロール
			if (raw->detail >= 4 && raw->detail <= 7) {
				me->type = 3;
				if (raw->detail == 4) me->scrollY = 1;
				else if (raw->detail == 5) me->scrollY = -1;
				else if (raw->detail == 6) me->scrollX = -1;
				else if (raw->detail == 7) me->scrollX = 1;
			} else {
				me->type = 1;
				me->button = raw->detail - 1; // 1=left→0, 2=middle→1, 3=right→2
				if (me->button == 1) me->button = 2; // middle
				else if (me->button == 2) me->button = 1; // right
			}
			g_evtCount++;
			break;
		case XI_RawButtonRelease:
			if (raw->detail >= 4 && raw->detail <= 7) {
				// スクロールリリースは無視
			} else {
				me->type = 2;
				me->button = raw->detail - 1;
				if (me->button == 1) me->button = 2;
				else if (me->button == 2) me->button = 1;
				g_evtCount++;
			}
			break;
		}

		XFreeEventData(g_dpy, &ev.xcookie);
	}
}

static int drainEventsLinux(MouseEvt *out, int maxOut) {
	pollEvents();
	int n = g_evtCount;
	if (n > maxOut) n = maxOut;
	for (int i = 0; i < n; i++) {
		out[i] = g_evtBuf[i];
	}
	int remaining = g_evtCount - n;
	for (int i = 0; i < remaining; i++) {
		g_evtBuf[i] = g_evtBuf[n + i];
	}
	g_evtCount = remaining;
	return n;
}

static void stopXI2(void) {
	g_running = 0;
	if (g_dpy) {
		XCloseDisplay(g_dpy);
		g_dpy = NULL;
	}
}

static void getScreenSizeLinux(int *w, int *h) {
	Display *dpy = g_dpy;
	if (!dpy) {
		*w = 0; *h = 0;
		return;
	}
	Screen *scr = DefaultScreenOfDisplay(dpy);
	*w = scr->width;
	*h = scr->height;
}

static void warpCursorLinux(int x, int y) {
	if (!g_dpy) return;
	XWarpPointer(g_dpy, None, DefaultRootWindow(g_dpy), 0, 0, 0, 0, x, y);
	XFlush(g_dpy);
}
*/
import "C"
import (
	"fmt"
	"time"
)

type linuxMouseHook struct {
	callback func(MouseHookEvent)
	stopCh   chan struct{}
	running  bool
}

func newMouseHook() MouseHook {
	return &linuxMouseHook{}
}

func (h *linuxMouseHook) Start(callback func(MouseHookEvent)) error {
	h.callback = callback

	ret := C.initXI2()
	if ret != 0 {
		return fmt.Errorf("XInput2 initialization failed (ret=%d)", int(ret))
	}

	h.stopCh = make(chan struct{})
	h.running = true

	go func() {
		var buf [64]C.MouseEvt
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-h.stopCh:
				return
			case <-ticker.C:
				n := int(C.drainEventsLinux(&buf[0], C.int(len(buf))))
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

	return nil
}

func (h *linuxMouseHook) Stop() {
	if !h.running {
		return
	}
	h.running = false
	close(h.stopCh)
	C.stopXI2()
}

func (h *linuxMouseHook) SetGrabbed(grabbed bool) {
	if grabbed {
		C.g_grabbed = 1
	} else {
		C.g_grabbed = 0
	}
}

func (h *linuxMouseHook) GetScreenSize() (width, height int) {
	var w, hh C.int
	C.getScreenSizeLinux(&w, &hh)
	return int(w), int(hh)
}

func (h *linuxMouseHook) WarpCursor(x, y int) {
	C.warpCursorLinux(C.int(x), C.int(y))
}
