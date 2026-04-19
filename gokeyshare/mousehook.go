package main

// MouseHookEventType はマウスフックイベントの種類
type MouseHookEventType int

const (
	MouseHookMove       MouseHookEventType = iota
	MouseHookButtonDown
	MouseHookButtonUp
	MouseHookScroll
)

// MouseHookEvent はOSレベルのマウスイベント
type MouseHookEvent struct {
	Type             MouseHookEventType
	X, Y             int // 絶対スクリーン座標
	DX, DY           int // 相対移動量
	Button           int // 0=left, 1=right, 2=middle
	ScrollX, ScrollY int // スクロール量
}

// Edge はスクリーン端の方向
type Edge int

const (
	EdgeNone   Edge = iota
	EdgeLeft
	EdgeRight
	EdgeTop
	EdgeBottom
)

// EdgeName は Edge の表示名を返す
func EdgeName(e Edge) string {
	switch e {
	case EdgeLeft:
		return "Left"
	case EdgeRight:
		return "Right"
	case EdgeTop:
		return "Top"
	case EdgeBottom:
		return "Bottom"
	default:
		return "None"
	}
}

// EdgeFromName は表示名から Edge を返す
func EdgeFromName(name string) Edge {
	switch name {
	case "Left":
		return EdgeLeft
	case "Right":
		return EdgeRight
	case "Top":
		return EdgeTop
	case "Bottom":
		return EdgeBottom
	default:
		return EdgeNone
	}
}

// MouseHook はOSレベルのグローバルマウスフックインターフェース
type MouseHook interface {
	// Start はフックを開始し、マウスイベントをコールバックに通知する
	Start(callback func(MouseHookEvent)) error
	// Stop はフックを停止する
	Stop()
	// SetGrabbed は true のときローカルマウスイベントを抑制し、
	// カーソルをエッジに固定する
	SetGrabbed(grabbed bool)
	// GetScreenSize はローカル画面サイズを返す
	GetScreenSize() (width, height int)
	// WarpCursor はカーソルを指定座標に移動する
	WarpCursor(x, y int)
}
