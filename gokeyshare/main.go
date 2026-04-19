package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"gokeyshare/InputShare"
	"image/color"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	flatbuffers "github.com/google/flatbuffers/go"
)

//go:embed translations
var translations embed.FS

// ビルド時に -ldflags "-X main.version=vX.Y.Z" で上書き可能
var version = "dev"

const writeTimeout = 10 * time.Second

var (
	connMu         sync.Mutex
	conn           net.Conn
	builderMu      sync.Mutex
	builder        = flatbuffers.NewBuilder(256)
	remotePlatform string // サーバ側の runtime.GOOS（"windows", "linux", "darwin" 等）
)

func main() {
	a := app.NewWithID("com.github.gokeyshare")

	// app.New() 後に登録することで、ロケール検出後にローカライザーへ確実に反映される
	if err := lang.AddTranslationsFS(translations, "translations"); err != nil {
		_ = err
	}

	w := a.NewWindow("gokeyshare")
	w.Resize(fyne.NewSize(480, 500))

	w.SetMainMenu(fyne.NewMainMenu(
		fyne.NewMenu("Help",
			fyne.NewMenuItem("About", func() {
				u, _ := url.Parse("https://github.com/cshara1/gokeyshare")
				dialog.ShowCustom("About gokeyshare", "OK",
					container.NewVBox(
						widget.NewLabel("gokeyshare "+version+"\n\nKeyboard input forwarding tool\nover TCP/TLS."),
						widget.NewHyperlink("GitHub", u),
					), w)
			}),
		),
	))

	servers := loadServers()
	addrEntry := widget.NewSelectEntry(serverAddrs(servers))
	addrEntry.SetPlaceHolder(lang.X("addr_placeholder", "host:port"))
	if len(servers) > 0 {
		addrEntry.SetText(servers[0].Addr)
	}

	statusLabel := widget.NewLabel(lang.X("status_disconnected", "● Disconnected"))

	logs := []string{}
	logLabel := widget.NewLabel("")
	addLog := func(s string) {
		logs = append(logs, s)
		if len(logs) > 8 {
			logs = logs[len(logs)-8:]
		}
		text := ""
		for _, l := range logs {
			text += l + "\n"
		}
		logLabel.SetText(text)
	}

	var connectBtn *widget.Button
	var kr *keyReceiver
	var retryCancel context.CancelFunc
	var lastAddr string          // 最後に接続したアドレス（自動再接続用）
	var startConnect func(string) // 前方宣言（onKey から参照するため）

	onKey := func(key string, mods []string) {
		connMu.Lock()
		c := conn
		connMu.Unlock()
		if c == nil {
			return
		}
		data := buildEvent(key, mods)
		if err := sendBuffer(c, data); err != nil {
			c.Close()
			connMu.Lock()
			conn = nil
			connMu.Unlock()
			// 自動再接続
			if lastAddr != "" {
				startConnect(lastAddr)
			}
			return
		}
		addLog(fmt.Sprintf("Key=%-12q Mods=%v", key, mods))
	}

	// 起動時のレイアウト: 直近接続サーバの保存値があればそれを優先、無ければグローバル既定
	savedLayout := a.Preferences().StringWithFallback("keyboard_layout", "us")
	if len(servers) > 0 && servers[0].Layout != "" {
		savedLayout = servers[0].Layout
	}
	screenKb := newScreenKeyboard(savedLayout, onKey, func() { w.Canvas().Focus(kr) })
	kr = newKeyReceiver(w, onKey)
	kr.screenKb = screenKb

	layoutSelect := widget.NewRadioGroup(skLayoutNames(), func(name string) {
		if name == "" {
			return
		}
		id := skLayoutIDByName(name)
		screenKb.SetLayout(id)
		a.Preferences().SetString("keyboard_layout", id)
		// 接続中なら現在のサーバ履歴にレイアウトを保存
		if lastAddr != "" {
			servers = upsertServer(lastAddr, id, servers)
		}
		fyne.Do(func() { w.Canvas().Focus(kr) })
	})
	layoutSelect.Horizontal = true
	layoutSelect.SetSelected(skLayoutNameByID(savedLayout))

	// completeConnection はダイアル成功後の共通処理
	completeConnection := func(newConn net.Conn, addr string) {
		if secret := os.Getenv("VKEYS_SECRET"); secret != "" {
			newConn.SetWriteDeadline(time.Now().Add(writeTimeout))
			newConn.Write([]byte(secret))
			newConn.SetWriteDeadline(time.Time{})
		}

		sendBuffer(newConn, buildPlatformQuery())
		platform := readPlatformInfo(newConn)
		remotePlatform = platform

		// リモート画面情報を取得
		sendBuffer(newConn, buildScreenInfoQuery())
		sw, sh, sf := readScreenInfo(newConn)
		remoteScreenW = sw
		remoteScreenH = sh
		remoteScaleFactor = sf

		fyne.Do(func() {
			connMu.Lock()
			conn = newConn
			connMu.Unlock()
			lastAddr = addr
			connectBtn.SetText(lang.X("btn_disconnect", "Disconnect"))
			connStatus := lang.X("status_connected", "● Connected: {{.Addr}}", map[string]any{"Addr": addr})
			if remoteScreenW > 0 && remoteScreenH > 0 {
				connStatus += fmt.Sprintf("  [%dx%d]", remoteScreenW, remoteScreenH)
			}
			statusLabel.SetText(connStatus)

			names := skLayoutNamesForPlatform(platform)
			layoutSelect.Options = names
			layoutSelect.Refresh()

			// このサーバに保存済みのレイアウトがあれば優先、無ければプラットフォームの先頭
			savedForServer := findServerLayout(servers, addr)
			savedName := skLayoutNameByID(savedForServer)
			selectName := ""
			if savedForServer != "" {
				for _, n := range names {
					if n == savedName {
						selectName = savedName
						break
					}
				}
			}
			if selectName == "" {
				// 現在の選択がプラットフォームに合致するならそのまま、しなければ先頭
				for _, n := range names {
					if n == layoutSelect.Selected {
						selectName = n
						break
					}
				}
				if selectName == "" && len(names) > 0 {
					selectName = names[0]
				}
			}
			if selectName != "" && selectName != layoutSelect.Selected {
				layoutSelect.SetSelected(selectName)
			} else if selectName != "" {
				// SetSelected が変化なしのときは明示的に screenKb を更新
				screenKb.SetLayout(skLayoutIDByName(selectName))
			}

			servers = upsertServer(addr, skLayoutIDByName(selectName), servers)
			addrEntry.SetOptions(serverAddrs(servers))
			w.Canvas().Focus(kr)
		})
	}

	// startConnect はリトライ付き接続を goroutine で開始
	startConnect = func(addr string) {
		if retryCancel != nil {
			retryCancel()
		}
		ctx, cancel := context.WithCancel(context.Background())
		retryCancel = cancel

		connectBtn.SetText(lang.X("btn_disconnect", "Disconnect"))

		go func() {
			backoff := time.Second
			const maxBackoff = 10 * time.Second
			const maxRetries = 5

			for attempt := 0; attempt < maxRetries; attempt++ {
				newConn, err := dial(addr)
				if err == nil {
					completeConnection(newConn, addr)
					return
				}

				// 接続失敗 → リトライ待機
				fyne.Do(func() {
					statusLabel.SetText(lang.X("status_retrying",
						"● Retrying in {{.Sec}}s...",
						map[string]any{"Sec": int(backoff.Seconds())}))
				})

				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				}

				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}

			// リトライ上限到達
			fyne.Do(func() {
				retryCancel = nil
				lastAddr = ""
				connectBtn.SetText(lang.X("btn_connect", "Connect"))
				statusLabel.SetText(lang.X("status_failed",
					"● Connection failed: {{.Err}}",
					map[string]any{"Err": fmt.Sprintf("max retries (%d) exceeded", maxRetries)}))
			})
		}()
	}

	connectBtn = widget.NewButton(lang.X("btn_connect", "Connect"), func() {
		connMu.Lock()
		c := conn
		connMu.Unlock()

		if c != nil || retryCancel != nil {
			// 切断 or リトライキャンセル
			if retryCancel != nil {
				retryCancel()
				retryCancel = nil
			}
			if c != nil {
				c.Close()
			}
			connMu.Lock()
			conn = nil
			connMu.Unlock()
			lastAddr = ""
			remotePlatform = ""
			remoteScreenW = 0
			remoteScreenH = 0
			remoteScaleFactor = 0
			connectBtn.SetText(lang.X("btn_connect", "Connect"))
			statusLabel.SetText(lang.X("status_disconnected", "● Disconnected"))
			layoutSelect.Options = skLayoutNames()
		layoutSelect.Refresh()
			return
		}

		startConnect(addrEntry.Text)
	})

	mouseToggle := widget.NewCheck(lang.X("btn_mouse", "Mouse"), func(on bool) {
		mouseEnabled = on
		fyne.Do(func() { w.Canvas().Focus(kr) })
	})

	pasteBtn := widget.NewButton(lang.X("btn_paste", "Paste to remote"), func() {
		connMu.Lock()
		c := conn
		connMu.Unlock()
		if c == nil {
			return
		}
		text := a.Clipboard().Content()
		if text == "" {
			return
		}
		data := buildClipboardEvent(text)
		if err := sendBuffer(c, data); err != nil {
			statusLabel.SetText(lang.X("status_error", "● Error: {{.Err}}", map[string]any{"Err": err.Error()}))
			connMu.Lock()
			conn = nil
			connMu.Unlock()
			connectBtn.SetText(lang.X("btn_connect", "Connect"))
			return
		}
		addLog(fmt.Sprintf("Clipboard: %d chars", len([]rune(text))))
		fyne.Do(func() { w.Canvas().Focus(kr) })
	})

	// --- 履歴クリアボタン ---
	clearBtn := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
		os.Remove(serversPath())
		servers = []serverEntry{}
		addrEntry.SetOptions([]string{})
		addrEntry.SetText("")
		fyne.Do(func() { w.Canvas().Focus(kr) })
	})

	// --- ログトグル ---
	logContainer := container.NewVBox(
		widget.NewSeparator(),
		widget.NewLabel(lang.X("log_header", "Send log:")),
		logLabel,
	)
	logContainer.Hide()
	var logToggle *widget.Button
	logToggle = widget.NewButton(lang.X("btn_log_show", "Log ▼"), func() {
		if logContainer.Visible() {
			logContainer.Hide()
			logToggle.SetText(lang.X("btn_log_show", "Log ▼"))
		} else {
			logContainer.Show()
			logToggle.SetText(lang.X("btn_log_hide", "Log ▲"))
		}
		fyne.Do(func() { w.Canvas().Focus(kr) })
	})

	// --- レイアウト構成 ---
	topBar := container.NewBorder(nil, nil, nil,
		container.NewHBox(clearBtn, connectBtn), addrEntry)

	bottomButtons := container.NewBorder(nil, nil, nil,
		logToggle, container.NewHBox(mouseToggle, pasteBtn))

	// krOverlay: keyReceiver をスクリーンキーボードの上に重ねて
	// マウスイベントを捕捉できるようにする（Stackで透明オーバーレイ）
	kbArea := container.NewStack(screenKb.Container, kr)

	center := container.NewBorder(layoutSelect, bottomButtons, nil, nil,
		kbArea)

	w.SetContent(container.NewBorder(
		topBar,       // top
		container.NewVBox(logContainer, statusLabel), // bottom
		nil, nil,
		center, // center expands
	))

	// --- ウィンドウ全体でキー入力を捕捉 ---
	// Canvas レベルのフォールバック: フォーカスが kr 以外にあるときもキーを転送
	// 注: SetOnKeyDown/SetOnKeyUp は全キーイベントを横取りするため使用しない
	//     （addrEntry 等の入力を妨げる）
	w.Canvas().SetOnTypedRune(func(r rune) {
		if !kr.focused {
			kr.TypedRune(r)
		}
	})
	w.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) {
		if !kr.focused {
			kr.TypedKey(ev)
		}
	})

	// 起動時に keyReceiver にフォーカスを設定（レンダリング完了後に実行）
	fyne.Do(func() {
		w.Canvas().Focus(kr)
	})

	// ウィンドウがアクティブになったとき接続の生存確認を行う
	a.Lifecycle().SetOnEnteredForeground(func() {
		connMu.Lock()
		c := conn
		connMu.Unlock()
		if c == nil {
			return
		}
		go func() {
			if !pingCheck(c) {
				c.Close()
				connMu.Lock()
				conn = nil
				connMu.Unlock()
				if lastAddr != "" {
					fyne.Do(func() {
						startConnect(lastAddr)
					})
				}
			}
		}()
	})

	w.ShowAndRun()
}

// --- keyReceiver: クリックでフォーカスを取得しキー入力を転送するウィジェット ---

type keyReceiver struct {
	widget.BaseWidget
	win            fyne.Window
	onKey          func(string, []string)
	focused        bool
	screenKb       *screenKeyboard
	tabJustPressed bool
	imeReacquire   bool   // IME切り替えキーによるフォーカス喪失時に再取得するフラグ
	pendingMod     string // 単独押しの候補となっている修飾キーID（空なら候補なし）
	// skipDownstream: KeyDown で送信した直後にスキップすべき後続イベント数。
	// fyne は同じ物理キー押下で TypedKey と TypedRune の両方を発火するため、
	// printable キー: 2、特殊キー: 1、Tab: 0（Tab は fyne が早期 return）。
	// 各 KeyDown でリセットし、TypedKey/TypedRune/TypedShortcut で 1 ずつ消費する。
	// キーリピート時は KeyDown が呼ばれないため自然と 0 のまま送信される。
	skipDownstream int
}

// standaloneModMap: 修飾キーの画面キーID → サーバーに送信するキー名
var standaloneModMap = map[string]string{
	"lshift": "shift", "rshift": "shift",
	"lctrl":  "ctrl",  "rctrl":  "ctrl",
	"lalt":   "alt",   "ralt":   "alt",
	"lsuper": "lsuper", "rsuper": "rsuper",
}

func newKeyReceiver(win fyne.Window, onKey func(string, []string)) *keyReceiver {
	k := &keyReceiver{win: win, onKey: onKey}
	k.ExtendBaseWidget(k)
	return k
}

func (k *keyReceiver) CreateRenderer() fyne.WidgetRenderer {
	// keyReceiver は非表示（フォーカス対象としてのみ存在）
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}

func (k *keyReceiver) MinSize() fyne.Size { return fyne.NewSize(0, 0) }

func (k *keyReceiver) Tapped(_ *fyne.PointEvent) {
	k.win.Canvas().Focus(k)
}

func (k *keyReceiver) FocusGained() { k.focused = true; k.Refresh() }
func (k *keyReceiver) FocusLost() {
	reacquire := k.tabJustPressed || k.imeReacquire
	k.tabJustPressed = false
	k.imeReacquire = false
	k.pendingMod = ""
	k.focused = false
	k.Refresh()
	if k.screenKb != nil {
		k.screenKb.ClearAllPressed()
	}
	if reacquire {
		// FocusLost 内での Focus() 呼び出しは再入問題を起こすため次のイベントループで実行
		fyne.Do(func() {
			k.win.Canvas().Focus(k)
		})
	}
}

func (k *keyReceiver) TypedRune(r rune) {
	// KeyDown で送信済みならスキップ
	if k.skipDownstream > 0 {
		k.skipDownstream--
		return
	}
	switch {
	case r == ' ':
		k.onKey("space", nil)
	case r >= 'a' && r <= 'z':
		k.onKey(string(r), nil)
	case r >= 'A' && r <= 'Z':
		k.onKey(string(r+32), []string{"shift"})
	case r >= '0' && r <= '9':
		k.onKey(string(r), nil)
	case r > ' ' && r <= '~':
		// その他の印字可能ASCII記号（. / , ; ' [ ] \ - = ` など）
		k.onKey(string(r), nil)
	}
}

// KeyDown は desktop.Keyable の実装。TypedKey/TypedRune より前に呼ばれる。
// fyne がフォーカス移動に使う Tab や、修飾キーと組み合わせた Alt+key をここで捕捉する。
func (k *keyReceiver) KeyDown(ev *fyne.KeyEvent) {
	// 新しいキー押下: 前回の skip 残数をクリア
	k.skipDownstream = 0

	if ev.Name == fyne.KeyTab {
		k.tabJustPressed = true
	}

	id := fyneNameToScreenID(ev.Name)
	if k.screenKb != nil && id != "" {
		k.screenKb.SetPressed(id, true)
	}

	// IME切り替えキー: CapsLock → サーバがWindowsなら hankaku を送信し、フォーカス再取得
	if id == "capslock" {
		k.imeReacquire = true
		if remotePlatform == "windows" {
			k.onKey("hankaku", nil)
		}
		return
	}

	// 修飾キー単独送信の追跡: 修飾キーなら候補に、それ以外ならクリア
	if _, isMod := standaloneModMap[id]; isMod {
		if k.pendingMod == "" {
			k.pendingMod = id
		} else {
			k.pendingMod = "" // 複数修飾キー同時押し → 単独送信しない
		}
	} else if id != "" {
		k.pendingMod = "" // 通常キー押下 → 修飾キーは修飾子として使用済み
	}

	mods := currentMods(k.win)

	switch ev.Name {
	case fyne.KeyTab:
		// fyne のフォーカス移動より先に横取り（fyne は Tab を早期 return するため後続なし）
		k.onKey("tab", mods)
	default:
		_, evIsMod := standaloneModMap[id]
		if evIsMod {
			// 修飾キー単独は KeyUp で処理
			return
		}

		var key string
		isSpecial := false
		if mapped := fyneKeyMap(ev.Name); mapped != "" {
			key = mapped
			isSpecial = true
		} else if name := string(ev.Name); len(name) == 1 {
			key = strings.ToLower(name)
		}
		if key != "" {
			k.onKey(key, mods)
			// 印字可能キー: TypedKey と TypedRune の両方が後続するため 2 をスキップ
			// 特殊キー: TypedKey のみのため 1 をスキップ
			// （Ctrl/Alt/Super 修飾時は fyne が TypedShortcut にルーティングするため 1）
			if isSpecial && key != "space" {
				k.skipDownstream = 1
			} else {
				k.skipDownstream = 2
			}
		}
		return
	}
}

func (k *keyReceiver) KeyUp(ev *fyne.KeyEvent) {
	id := fyneNameToScreenID(ev.Name)
	if k.screenKb != nil && id != "" {
		k.screenKb.SetPressed(id, false)
	}
	// 修飾キー単独送信: KeyDown〜KeyUp の間に他のキーが押されなかった場合
	if keyName, isMod := standaloneModMap[id]; isMod && k.pendingMod == id {
		k.pendingMod = ""
		k.onKey(keyName, nil)
	}
}

func (k *keyReceiver) TypedKey(ev *fyne.KeyEvent) {
	if k.skipDownstream > 0 {
		k.skipDownstream--
		return
	}
	mods := currentMods(k.win)
	// Alt+key は KeyDown で処理済みのためスキップ
	for _, m := range mods {
		if m == "alt" {
			return
		}
	}
	if key := fyneKeyMap(ev.Name); key != "" {
		k.onKey(key, mods)
	}
}

// TypedShortcut は Ctrl+C/V/Z など fyne が横取りするショートカットを転送する
func (k *keyReceiver) TypedShortcut(s fyne.Shortcut) {
	if k.skipDownstream > 0 {
		k.skipDownstream = 0 // ショートカット経路では後続イベントなし
		return
	}
	var key string
	var mods []string

	switch sc := s.(type) {
	case *desktop.CustomShortcut:
		name := strings.ToLower(string(sc.KeyName))
		key = fyneKeyMap(fyne.KeyName(strings.ToUpper(name)))
		if key == "" && len(name) == 1 {
			key = name
		}
		mods = modifierList(sc.Modifier)
	case *fyne.ShortcutCopy:
		key, mods = "c", []string{"ctrl"}
	case *fyne.ShortcutPaste:
		key, mods = "v", []string{"ctrl"}
	case *fyne.ShortcutCut:
		key, mods = "x", []string{"ctrl"}
	case *fyne.ShortcutUndo:
		key, mods = "z", []string{"ctrl"}
	case *fyne.ShortcutSelectAll:
		key, mods = "a", []string{"ctrl"}
	}

	if key != "" {
		k.onKey(key, mods)
	}
}

// currentMods は現在押されている修飾キーを返す
func currentMods(win fyne.Window) []string {
	dd, ok := fyne.CurrentApp().Driver().(desktop.Driver)
	if !ok {
		return nil
	}
	return modifierList(dd.CurrentKeyModifiers())
}

func modifierList(m fyne.KeyModifier) []string {
	var mods []string
	if m&fyne.KeyModifierShift   != 0 { mods = append(mods, "shift") }
	if m&fyne.KeyModifierControl != 0 { mods = append(mods, "ctrl")  }
	if m&fyne.KeyModifierAlt     != 0 { mods = append(mods, "alt")   }
	if m&fyne.KeyModifierSuper   != 0 { mods = append(mods, "super") }
	return mods
}

func fyneKeyMap(name fyne.KeyName) string {
	switch name {
	case fyne.KeyReturn:    return "enter"
	case fyne.KeyBackspace: return "backspace"
	case fyne.KeyDelete:    return "delete"
	case fyne.KeyEscape:    return "escape"
	case fyne.KeyTab:  return "tab"
	case fyne.KeySpace: return "space"
	case fyne.KeyUp:   return "up"
	case fyne.KeyDown:      return "down"
	case fyne.KeyLeft:      return "left"
	case fyne.KeyRight:     return "right"
	case fyne.KeyHome:      return "home"
	case fyne.KeyEnd:       return "end"
	case fyne.KeyPageUp:    return "pageup"
	case fyne.KeyPageDown:  return "pagedown"
	case fyne.KeyInsert:    return "insert"
	case fyne.KeyF1:  return "f1"
	case fyne.KeyF2:  return "f2"
	case fyne.KeyF3:  return "f3"
	case fyne.KeyF4:  return "f4"
	case fyne.KeyF5:  return "f5"
	case fyne.KeyF6:  return "f6"
	case fyne.KeyF7:  return "f7"
	case fyne.KeyF8:  return "f8"
	case fyne.KeyF9:  return "f9"
	case fyne.KeyF10: return "f10"
	case fyne.KeyF11: return "f11"
	case fyne.KeyF12: return "f12"
	}
	return ""
}

// --- keyReceiver: マウスイベント (desktop.Mouseable, desktop.Hoverable) ---

func (k *keyReceiver) MouseDown(ev *desktop.MouseEvent) {
	if !mouseEnabled {
		return
	}
	sendMouseEvent(buildMouseButtonEvent(InputShare.EventTypeMouseDown, fyneButtonToMouse(ev.Button)))
}

func (k *keyReceiver) MouseUp(ev *desktop.MouseEvent) {
	if !mouseEnabled {
		return
	}
	sendMouseEvent(buildMouseButtonEvent(InputShare.EventTypeMouseUp, fyneButtonToMouse(ev.Button)))
}

func (k *keyReceiver) MouseIn(ev *desktop.MouseEvent) {}
func (k *keyReceiver) MouseOut()                      {}

func (k *keyReceiver) MouseMoved(ev *desktop.MouseEvent) {
	if !mouseEnabled || remoteScreenW == 0 || remoteScreenH == 0 {
		return
	}
	now := time.Now()
	if now.Sub(lastMouseSend) < mouseThrottle {
		return
	}
	lastMouseSend = now

	// ウィジェットサイズに対する比例座標でリモート画面にマッピング
	size := k.Size()
	if size.Width <= 0 || size.Height <= 0 {
		return
	}
	x := int32(float32(ev.Position.X) / size.Width * float32(remoteScreenW))
	y := int32(float32(ev.Position.Y) / size.Height * float32(remoteScreenH))
	sendMouseEvent(buildMouseMoveEvent(x, y, false))
}

func (k *keyReceiver) Scrolled(ev *fyne.ScrollEvent) {
	if !mouseEnabled {
		return
	}
	dx := int32(ev.Scrolled.DX)
	dy := int32(ev.Scrolled.DY)
	if dx == 0 && dy == 0 {
		return
	}
	sendMouseEvent(buildMouseScrollEvent(dx, dy))
}

// --- server history ---

const maxServers = 10

// serverEntry は履歴1件分。Layout は最後にこのサーバで選択したキーボードレイアウト ID。
type serverEntry struct {
	Addr   string `json:"addr"`
	Layout string `json:"layout,omitempty"`
}

func serversPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "gokeyshare", "servers.json")
}

// loadServers は履歴を読み込む。旧形式（[]string）も後方互換でサポート。
func loadServers() []serverEntry {
	data, err := os.ReadFile(serversPath())
	if err != nil {
		return []serverEntry{}
	}
	// 新形式: [{addr, layout}, ...]
	var entries []serverEntry
	if err := json.Unmarshal(data, &entries); err == nil && len(entries) > 0 && entries[0].Addr != "" {
		return entries
	}
	// 旧形式: ["addr1", "addr2", ...]
	var addrs []string
	if err := json.Unmarshal(data, &addrs); err == nil {
		out := make([]serverEntry, 0, len(addrs))
		for _, a := range addrs {
			if a != "" {
				out = append(out, serverEntry{Addr: a})
			}
		}
		return out
	}
	return []serverEntry{}
}

// serverAddrs はアドレスのみのスライスを返す（UI 用）
func serverAddrs(entries []serverEntry) []string {
	addrs := make([]string, len(entries))
	for i, e := range entries {
		addrs[i] = e.Addr
	}
	return addrs
}

// findServerLayout は addr に対する保存済みレイアウト ID を返す（無ければ空）
func findServerLayout(entries []serverEntry, addr string) string {
	for _, e := range entries {
		if e.Addr == addr {
			return e.Layout
		}
	}
	return ""
}

// upsertServer は addr を先頭に追加（または昇格）し、重複排除・上限適用する。
// layout が空でなければそのレイアウトを保存する。空の場合は既存のレイアウトを保持。
func upsertServer(addr, layout string, current []serverEntry) []serverEntry {
	existingLayout := findServerLayout(current, addr)
	if layout == "" {
		layout = existingLayout
	}
	next := []serverEntry{{Addr: addr, Layout: layout}}
	for _, e := range current {
		if e.Addr != addr {
			next = append(next, e)
		}
	}
	if len(next) > maxServers {
		next = next[:maxServers]
	}
	persistServers(next)
	return next
}

func persistServers(entries []serverEntry) {
	path := serversPath()
	os.MkdirAll(filepath.Dir(path), 0755)
	if data, err := json.Marshal(entries); err == nil {
		os.WriteFile(path, data, 0644)
	}
}

// --- platform query ---

func buildPlatformQuery() []byte {
	builderMu.Lock()
	defer builderMu.Unlock()

	builder.Reset()
	keyStr := builder.CreateString("")
	InputShare.KeyEventStartModifiersVector(builder, 0)
	modsVec := builder.EndVector(0)
	InputShare.KeyEventStart(builder)
	InputShare.KeyEventAddKey(builder, keyStr)
	InputShare.KeyEventAddModifiers(builder, modsVec)
	ke := InputShare.KeyEventEnd(builder)

	InputShare.EventStart(builder)
	InputShare.EventAddEventType(builder, InputShare.EventTypePlatformQuery)
	InputShare.EventAddKeyEvent(builder, ke)
	ev := InputShare.EventEnd(builder)

	builder.Finish(ev)
	return builder.FinishedBytes()
}

func readPlatformInfo(c net.Conn) string {
	c.SetReadDeadline(time.Now().Add(time.Second))
	defer c.SetReadDeadline(time.Time{})

	sizeBuf := make([]byte, 4)
	if _, err := io.ReadFull(c, sizeBuf); err != nil {
		return ""
	}
	msgSize := binary.LittleEndian.Uint32(sizeBuf)
	if msgSize == 0 || msgSize > 1024 {
		return ""
	}
	msgBuf := make([]byte, msgSize)
	if _, err := io.ReadFull(c, msgBuf); err != nil {
		return ""
	}

	event := InputShare.GetRootAsEvent(msgBuf, 0)
	if event.EventType() != InputShare.EventTypePlatformInfo {
		return ""
	}
	ke := new(InputShare.KeyEvent)
	event.KeyEvent(ke)
	return string(ke.Key())
}

// --- ping/pong ---

func buildPing() []byte {
	builderMu.Lock()
	defer builderMu.Unlock()

	builder.Reset()
	InputShare.EventStart(builder)
	InputShare.EventAddEventType(builder, InputShare.EventTypePing)
	ev := InputShare.EventEnd(builder)
	builder.Finish(ev)
	return builder.FinishedBytes()
}

// pingCheck は Ping を送信し Pong 応答を待つ。成功なら true、失敗なら false。
func pingCheck(c net.Conn) bool {
	if err := sendBuffer(c, buildPing()); err != nil {
		return false
	}
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	defer c.SetReadDeadline(time.Time{})

	sizeBuf := make([]byte, 4)
	if _, err := io.ReadFull(c, sizeBuf); err != nil {
		return false
	}
	msgSize := binary.LittleEndian.Uint32(sizeBuf)
	if msgSize == 0 || msgSize > 1024 {
		return false
	}
	msgBuf := make([]byte, msgSize)
	if _, err := io.ReadFull(c, msgBuf); err != nil {
		return false
	}
	event := InputShare.GetRootAsEvent(msgBuf, 0)
	return event.EventType() == InputShare.EventTypePong
}

// --- clipboard ---

func buildClipboardEvent(text string) []byte {
	builderMu.Lock()
	defer builderMu.Unlock()

	builder.Reset()
	textStr := builder.CreateString(text)

	InputShare.KeyEventStartModifiersVector(builder, 0)
	modsVec := builder.EndVector(0)

	InputShare.KeyEventStart(builder)
	InputShare.KeyEventAddKey(builder, textStr)
	InputShare.KeyEventAddModifiers(builder, modsVec)
	ke := InputShare.KeyEventEnd(builder)

	InputShare.EventStart(builder)
	InputShare.EventAddEventType(builder, InputShare.EventTypeClipboard)
	InputShare.EventAddKeyEvent(builder, ke)
	ev := InputShare.EventEnd(builder)

	builder.Finish(ev)
	return builder.FinishedBytes()
}

// --- mouse events ---

// リモート画面情報
var (
	remoteScreenW     int32
	remoteScreenH     int32
	remoteScaleFactor float32
	mouseEnabled      bool      // マウス転送ON/OFF
	lastMouseSend     time.Time // スロットリング用
)

const mouseThrottle = 8 * time.Millisecond // ~120Hz

func buildMouseMoveEvent(x, y int32, relative bool) []byte {
	builderMu.Lock()
	defer builderMu.Unlock()

	builder.Reset()
	InputShare.MouseEventStart(builder)
	InputShare.MouseEventAddX(builder, x)
	InputShare.MouseEventAddY(builder, y)
	InputShare.MouseEventAddIsRelative(builder, relative)
	me := InputShare.MouseEventEnd(builder)

	InputShare.EventStart(builder)
	InputShare.EventAddEventType(builder, InputShare.EventTypeMouseMove)
	InputShare.EventAddMouseEvent(builder, me)
	ev := InputShare.EventEnd(builder)

	builder.Finish(ev)
	return builder.FinishedBytes()
}

func buildMouseButtonEvent(eventType InputShare.EventType, button InputShare.MouseButton) []byte {
	builderMu.Lock()
	defer builderMu.Unlock()

	builder.Reset()
	InputShare.MouseEventStart(builder)
	InputShare.MouseEventAddButton(builder, button)
	me := InputShare.MouseEventEnd(builder)

	InputShare.EventStart(builder)
	InputShare.EventAddEventType(builder, eventType)
	InputShare.EventAddMouseEvent(builder, me)
	ev := InputShare.EventEnd(builder)

	builder.Finish(ev)
	return builder.FinishedBytes()
}

func buildMouseScrollEvent(dx, dy int32) []byte {
	builderMu.Lock()
	defer builderMu.Unlock()

	builder.Reset()
	InputShare.MouseEventStart(builder)
	InputShare.MouseEventAddScrollDx(builder, dx)
	InputShare.MouseEventAddScrollDy(builder, dy)
	me := InputShare.MouseEventEnd(builder)

	InputShare.EventStart(builder)
	InputShare.EventAddEventType(builder, InputShare.EventTypeMouseScroll)
	InputShare.EventAddMouseEvent(builder, me)
	ev := InputShare.EventEnd(builder)

	builder.Finish(ev)
	return builder.FinishedBytes()
}

func buildScreenInfoQuery() []byte {
	builderMu.Lock()
	defer builderMu.Unlock()

	builder.Reset()
	InputShare.EventStart(builder)
	InputShare.EventAddEventType(builder, InputShare.EventTypeScreenInfoQuery)
	ev := InputShare.EventEnd(builder)
	builder.Finish(ev)
	return builder.FinishedBytes()
}

func readScreenInfo(c net.Conn) (w, h int32, scale float32) {
	c.SetReadDeadline(time.Now().Add(time.Second))
	defer c.SetReadDeadline(time.Time{})

	sizeBuf := make([]byte, 4)
	if _, err := io.ReadFull(c, sizeBuf); err != nil {
		return 0, 0, 0
	}
	msgSize := binary.LittleEndian.Uint32(sizeBuf)
	if msgSize == 0 || msgSize > 1024 {
		return 0, 0, 0
	}
	msgBuf := make([]byte, msgSize)
	if _, err := io.ReadFull(c, msgBuf); err != nil {
		return 0, 0, 0
	}

	event := InputShare.GetRootAsEvent(msgBuf, 0)
	if event.EventType() != InputShare.EventTypeScreenInfoReply {
		return 0, 0, 0
	}
	si := new(InputShare.ScreenInfo)
	if event.ScreenInfo(si) == nil {
		return 0, 0, 0
	}
	return si.Width(), si.Height(), si.ScaleFactor()
}

// sendMouseEvent はマウスイベントを送信する共通ヘルパー
func sendMouseEvent(data []byte) {
	connMu.Lock()
	c := conn
	connMu.Unlock()
	if c == nil {
		return
	}
	sendBuffer(c, data)
}

// fyneButtonToMouse は Fyne のマウスボタンを InputShare.MouseButton に変換する
func fyneButtonToMouse(btn desktop.MouseButton) InputShare.MouseButton {
	switch btn {
	case desktop.MouseButtonSecondary:
		return InputShare.MouseButtonRight
	case desktop.MouseButtonTertiary:
		return InputShare.MouseButtonMiddle
	default:
		return InputShare.MouseButtonLeft
	}
}

// --- networking ---

func dial(addr string) (net.Conn, error) {
	if os.Getenv("VKEYS_TLS") != "1" {
		return net.Dial("tcp", addr)
	}

	tlsCfg := &tls.Config{}
	if caFile := os.Getenv("VKEYS_CA"); caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("CA証明書の読み込みエラー: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA証明書の解析に失敗しました")
		}
		tlsCfg.RootCAs = pool
	} else {
		tlsCfg.InsecureSkipVerify = true
	}

	return tls.Dial("tcp", addr, tlsCfg)
}

func sendBuffer(c net.Conn, buf []byte) error {
	sizeBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(sizeBuf, uint32(len(buf)))
	c.SetWriteDeadline(time.Now().Add(writeTimeout))
	defer c.SetWriteDeadline(time.Time{})
	if _, err := c.Write(sizeBuf); err != nil {
		return fmt.Errorf("サイズ送信エラー: %w", err)
	}
	if _, err := c.Write(buf); err != nil {
		return fmt.Errorf("データ送信エラー: %w", err)
	}
	return nil
}

func buildEvent(key string, mods []string) []byte {
	builderMu.Lock()
	defer builderMu.Unlock()

	builder.Reset()
	keyStr := builder.CreateString(key)
	modOffsets := make([]flatbuffers.UOffsetT, len(mods))
	for i, m := range mods {
		modOffsets[i] = builder.CreateString(m)
	}
	InputShare.KeyEventStartModifiersVector(builder, len(modOffsets))
	for i := len(modOffsets) - 1; i >= 0; i-- {
		builder.PrependUOffsetT(modOffsets[i])
	}
	modsVec := builder.EndVector(len(modOffsets))

	InputShare.KeyEventStart(builder)
	InputShare.KeyEventAddKey(builder, keyStr)
	InputShare.KeyEventAddModifiers(builder, modsVec)
	ke := InputShare.KeyEventEnd(builder)

	InputShare.EventStart(builder)
	InputShare.EventAddEventType(builder, InputShare.EventTypeKeyDown)
	InputShare.EventAddKeyEvent(builder, ke)
	ev := InputShare.EventEnd(builder)

	builder.Finish(ev)
	return builder.FinishedBytes()
}
