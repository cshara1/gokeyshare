package main

import (
	"gokeyshare/InputShare"
	"log"
	"sync"
	"time"
)

// Forwarder はシームレス画面切替の状態を管理する
type Forwarder struct {
	mu          sync.Mutex
	hook        MouseHook
	forwarding  bool
	edge        Edge       // 切替を発生させるエッジ
	onKey       func(string, []string) // キーボードイベント転送コールバック
	onStateChange func(forwarding bool) // 状態変更通知（UI更新用）

	// ローカル画面
	localW, localH int
	// リモート画面
	remoteW, remoteH int32

	// スロットリング
	lastSend time.Time
}

const forwarderThrottle = 8 * time.Millisecond

// NewForwarder は新しい Forwarder を作成する
func NewForwarder(onKey func(string, []string), onStateChange func(bool)) *Forwarder {
	return &Forwarder{
		hook:          newMouseHook(),
		edge:          EdgeRight, // デフォルト: 右端
		onKey:         onKey,
		onStateChange: onStateChange,
	}
}

// SetEdge は切替エッジを設定する
func (f *Forwarder) SetEdge(e Edge) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edge = e
}

// SetRemoteScreen はリモート画面サイズを設定する
func (f *Forwarder) SetRemoteScreen(w, h int32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.remoteW = w
	f.remoteH = h
}

// Start はマウスフックを開始する
func (f *Forwarder) Start() error {
	f.mu.Lock()
	f.localW, f.localH = f.hook.GetScreenSize()
	f.mu.Unlock()

	return f.hook.Start(f.handleMouseEvent)
}

// Stop はマウスフックを停止する
func (f *Forwarder) Stop() {
	f.mu.Lock()
	wasForwarding := f.forwarding
	f.forwarding = false
	f.mu.Unlock()

	f.hook.SetGrabbed(false)
	f.hook.Stop()

	if wasForwarding && f.onStateChange != nil {
		f.onStateChange(false)
	}
}

// IsForwarding は転送中かどうかを返す
func (f *Forwarder) IsForwarding() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.forwarding
}

// handleMouseEvent はフックからのマウスイベントを処理する
func (f *Forwarder) handleMouseEvent(evt MouseHookEvent) {
	f.mu.Lock()
	forwarding := f.forwarding
	edge := f.edge
	localW := f.localW
	localH := f.localH
	remoteW := f.remoteW
	remoteH := f.remoteH
	f.mu.Unlock()

	if !forwarding {
		// エッジ検知
		if edge == EdgeNone || remoteW == 0 || remoteH == 0 {
			return
		}
		if evt.Type != MouseHookMove {
			return
		}
		if checkEdge(evt.X, evt.Y, localW, localH, edge) {
			f.enterForwarding(edge, localW, localH, remoteW, remoteH)
		}
		return
	}

	// 転送中: マウスイベントをリモートに送信
	switch evt.Type {
	case MouseHookMove:
		now := time.Now()
		if now.Sub(f.lastSend) < forwarderThrottle {
			return
		}
		f.lastSend = now

		// 相対移動をリモートに送信
		if evt.DX == 0 && evt.DY == 0 {
			return
		}
		sendMouseEvent(buildMouseMoveEvent(int32(evt.DX), int32(evt.DY), true))

	case MouseHookButtonDown:
		btn := hookButtonToMouse(evt.Button)
		sendMouseEvent(buildMouseButtonEvent(InputShare.EventTypeMouseDown, btn))

	case MouseHookButtonUp:
		btn := hookButtonToMouse(evt.Button)
		sendMouseEvent(buildMouseButtonEvent(InputShare.EventTypeMouseUp, btn))

	case MouseHookScroll:
		if evt.ScrollX != 0 || evt.ScrollY != 0 {
			sendMouseEvent(buildMouseScrollEvent(int32(evt.ScrollX), int32(evt.ScrollY)))
		}
	}
}

// enterForwarding は転送モードに入る
func (f *Forwarder) enterForwarding(edge Edge, localW, localH int, remoteW, remoteH int32) {
	f.mu.Lock()
	if f.forwarding {
		f.mu.Unlock()
		return
	}
	f.forwarding = true
	f.mu.Unlock()

	log.Printf("シームレス転送開始 (edge=%s)", EdgeName(edge))

	// カーソルをエッジに固定
	warpX, warpY := edgeWarpPosition(edge, localW, localH)
	f.hook.WarpCursor(warpX, warpY)
	f.hook.SetGrabbed(true)

	// リモートカーソルを対向エッジに移動
	entryX, entryY := remoteEntryPosition(edge, remoteW, remoteH)
	sendMouseEvent(buildMouseMoveEvent(entryX, entryY, false))

	if f.onStateChange != nil {
		f.onStateChange(true)
	}
}

// ExitForwarding は転送モードを終了する（ホットキーや戻りエッジから呼ばれる）
func (f *Forwarder) ExitForwarding() {
	f.mu.Lock()
	if !f.forwarding {
		f.mu.Unlock()
		return
	}
	f.forwarding = false
	f.mu.Unlock()

	log.Printf("シームレス転送終了")

	f.hook.SetGrabbed(false)

	if f.onStateChange != nil {
		f.onStateChange(false)
	}
}

// --- ヘルパー ---

// checkEdge はカーソルがエッジに到達したかを判定する
func checkEdge(x, y, screenW, screenH int, edge Edge) bool {
	switch edge {
	case EdgeRight:
		return x >= screenW-1
	case EdgeLeft:
		return x <= 0
	case EdgeTop:
		return y <= 0
	case EdgeBottom:
		return y >= screenH-1
	default:
		return false
	}
}

// edgeWarpPosition はエッジ上の固定位置を返す
func edgeWarpPosition(edge Edge, screenW, screenH int) (x, y int) {
	switch edge {
	case EdgeRight:
		return screenW - 2, screenH / 2
	case EdgeLeft:
		return 1, screenH / 2
	case EdgeTop:
		return screenW / 2, 1
	case EdgeBottom:
		return screenW / 2, screenH - 2
	default:
		return screenW / 2, screenH / 2
	}
}

// remoteEntryPosition はリモート画面での入口位置を返す
func remoteEntryPosition(edge Edge, remoteW, remoteH int32) (x, y int32) {
	switch edge {
	case EdgeRight:
		// ローカル右端 → リモート左端から入る
		return 1, remoteH / 2
	case EdgeLeft:
		// ローカル左端 → リモート右端から入る
		return remoteW - 2, remoteH / 2
	case EdgeTop:
		// ローカル上端 → リモート下端から入る
		return remoteW / 2, remoteH - 2
	case EdgeBottom:
		// ローカル下端 → リモート上端から入る
		return remoteW / 2, 1
	default:
		return remoteW / 2, remoteH / 2
	}
}

// hookButtonToMouse は MouseHookEvent のボタン番号を InputShare.MouseButton に変換する
func hookButtonToMouse(button int) InputShare.MouseButton {
	switch button {
	case 1:
		return InputShare.MouseButtonRight
	case 2:
		return InputShare.MouseButtonMiddle
	default:
		return InputShare.MouseButtonLeft
	}
}
