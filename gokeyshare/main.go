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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	flatbuffers "github.com/google/flatbuffers/go"
)

//go:embed translations
var translations embed.FS

const writeTimeout = 10 * time.Second

var (
	connMu    sync.Mutex
	conn      net.Conn
	builderMu sync.Mutex
	builder   = flatbuffers.NewBuilder(256)
)

func main() {
	a := app.New()

	// app.New() 後に登録することで、ロケール検出後にローカライザーへ確実に反映される
	if err := lang.AddTranslationsFS(translations, "translations"); err != nil {
		_ = err
	}

	w := a.NewWindow("gokeyshare")
	w.Resize(fyne.NewSize(480, 500))

	servers := loadServers()
	addrEntry := widget.NewSelectEntry(servers)
	addrEntry.SetPlaceHolder(lang.X("addr_placeholder", "host:port"))
	if len(servers) > 0 {
		addrEntry.SetText(servers[0])
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

	onKey := func(key string, mods []string) {
		connMu.Lock()
		c := conn
		connMu.Unlock()
		if c == nil {
			return
		}
		data := buildEvent(key, mods)
		if err := sendBuffer(c, data); err != nil {
			statusLabel.SetText(lang.X("status_error", "● Error: {{.Err}}", map[string]any{"Err": err.Error()}))
			connMu.Lock()
			conn = nil
			connMu.Unlock()
			connectBtn.SetText(lang.X("btn_connect", "Connect"))
			return
		}
		addLog(fmt.Sprintf("Key=%-12q Mods=%v", key, mods))
	}

	savedLayout := a.Preferences().StringWithFallback("keyboard_layout", "us")
	screenKb := newScreenKeyboard(savedLayout, onKey, func() { w.Canvas().Focus(kr) })
	kr = newKeyReceiver(w, onKey)
	kr.screenKb = screenKb

	layoutSelect := widget.NewSelect(skLayoutNames(), func(name string) {
		id := skLayoutIDByName(name)
		screenKb.SetLayout(id)
		a.Preferences().SetString("keyboard_layout", id)
		fyne.Do(func() { w.Canvas().Focus(kr) })
	})
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

		fyne.Do(func() {
			connMu.Lock()
			conn = newConn
			connMu.Unlock()
			connectBtn.SetText(lang.X("btn_disconnect", "Disconnect"))
			statusLabel.SetText(lang.X("status_connected", "● Connected: {{.Addr}}", map[string]any{"Addr": addr}))

			names := skLayoutNamesForPlatform(platform)
			layoutSelect.SetOptions(names)
			currentValid := false
			for _, n := range names {
				if n == layoutSelect.Selected {
					currentValid = true
					break
				}
			}
			if !currentValid && len(names) > 0 {
				layoutSelect.SetSelected(names[0])
			}

			servers = saveServer(addr, servers)
			addrEntry.SetOptions(servers)
			w.Canvas().Focus(kr)
		})
	}

	// startConnect はリトライ付き接続を goroutine で開始
	startConnect := func(addr string) {
		if retryCancel != nil {
			retryCancel()
		}
		ctx, cancel := context.WithCancel(context.Background())
		retryCancel = cancel

		connectBtn.SetText(lang.X("btn_disconnect", "Disconnect"))

		go func() {
			backoff := time.Second
			const maxBackoff = 10 * time.Second

			for {
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
			connectBtn.SetText(lang.X("btn_connect", "Connect"))
			statusLabel.SetText(lang.X("status_disconnected", "● Disconnected"))
			layoutSelect.SetOptions(skLayoutNames())
			return
		}

		startConnect(addrEntry.Text)
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
		servers = []string{}
		addrEntry.SetOptions(servers)
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
		logToggle, pasteBtn)

	center := container.NewBorder(layoutSelect, bottomButtons, nil, nil,
		screenKb.Container)

	w.SetContent(container.NewBorder(
		topBar,       // top
		container.NewVBox(logContainer, statusLabel), // bottom
		nil, nil,
		center, // center expands
	))

	// --- ウィンドウ全体でキー入力を捕捉 ---
	// Canvas レベルのフォールバック: フォーカスが kr 以外にあるときもキーを転送
	w.Canvas().SetOnTypedRune(func(r rune) { kr.TypedRune(r) })
	w.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) { kr.TypedKey(ev) })
	if dc, ok := w.Canvas().(desktop.Canvas); ok {
		dc.SetOnKeyDown(func(ev *fyne.KeyEvent) { kr.KeyDown(ev) })
		dc.SetOnKeyUp(func(ev *fyne.KeyEvent) { kr.KeyUp(ev) })
	}

	// 起動時に keyReceiver にフォーカスを設定
	w.Canvas().Focus(kr)

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
	pendingMod     string // 単独押しの候補となっている修飾キーID（空なら候補なし）
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
	reacquire := k.tabJustPressed
	k.tabJustPressed = false
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
	if ev.Name == fyne.KeyTab {
		k.tabJustPressed = true
	}

	id := fyneNameToScreenID(ev.Name)
	if k.screenKb != nil && id != "" {
		k.screenKb.SetPressed(id, true)
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
		// fyne のフォーカス移動より先に横取り
		k.onKey("tab", mods)
	default:
		// Alt が押されている場合、TypedRune では OS 変換後の文字が届くため
		// ここで生のキー名を使って転送する
		if len(mods) > 0 {
			for _, m := range mods {
				if m == "alt" {
					if key := fyneKeyMap(ev.Name); key != "" {
						k.onKey(key, mods)
						return
					}
					// 単一文字キー（a-z, 0-9 など）
					name := string(ev.Name)
					if len(name) == 1 {
						k.onKey(strings.ToLower(name), mods)
					}
					return
				}
			}
		}
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
	// KeySpace は TypedRune で処理するためここでは除外
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

// keyReceiverRenderer は廃止（keyReceiver は非表示ウィジェット）

// --- server history ---

const maxServers = 10

func serversPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "gokeyshare", "servers.json")
}

func loadServers() []string {
	data, err := os.ReadFile(serversPath())
	if err != nil {
		return []string{}
	}
	var servers []string
	if err := json.Unmarshal(data, &servers); err != nil {
		return []string{}
	}
	return servers
}

// saveServer は addr を先頭に追加し、重複排除・上限適用して保存する。
// 更新後のリストを返す。
func saveServer(addr string, current []string) []string {
	// 重複を除いた新リストを作成（addr を先頭に）
	seen := map[string]bool{addr: true}
	next := []string{addr}
	for _, s := range current {
		if !seen[s] {
			seen[s] = true
			next = append(next, s)
		}
	}
	if len(next) > maxServers {
		next = next[:maxServers]
	}

	path := serversPath()
	os.MkdirAll(filepath.Dir(path), 0755)
	if data, err := json.Marshal(next); err == nil {
		os.WriteFile(path, data, 0644)
	}
	return next
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
