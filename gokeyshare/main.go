package main

import (
	"crypto/tls"
	"crypto/x509"
	"embed"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"gokeyshare/InputShare"
	"image/color"
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
	w.Resize(fyne.NewSize(420, 380))
	w.SetFixedSize(true)

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

	kr := newKeyReceiver(w, func(key string, mods []string) {
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
	})

	connectBtn = widget.NewButton(lang.X("btn_connect", "Connect"), func() {
		connMu.Lock()
		c := conn
		connMu.Unlock()

		if c != nil {
			c.Close()
			connMu.Lock()
			conn = nil
			connMu.Unlock()
			connectBtn.SetText(lang.X("btn_connect", "Connect"))
			statusLabel.SetText(lang.X("status_disconnected", "● Disconnected"))
			return
		}

		addr := addrEntry.Text
		newConn, err := dial(addr)
		if err != nil {
			statusLabel.SetText(lang.X("status_failed", "● Connection failed: {{.Err}}", map[string]any{"Err": err.Error()}))
			return
		}

		if secret := os.Getenv("VKEYS_SECRET"); secret != "" {
			newConn.SetWriteDeadline(time.Now().Add(writeTimeout))
			newConn.Write([]byte(secret))
			newConn.SetWriteDeadline(time.Time{})
		}

		connMu.Lock()
		conn = newConn
		connMu.Unlock()
		connectBtn.SetText(lang.X("btn_disconnect", "Disconnect"))
		statusLabel.SetText(lang.X("status_connected", "● Connected: {{.Addr}}", map[string]any{"Addr": addr}))

		// 接続成功したアドレスを履歴に保存
		servers = saveServer(addr, servers)
		addrEntry.SetOptions(servers)
	})

	w.SetContent(container.NewVBox(
		container.NewBorder(nil, nil, nil, connectBtn, addrEntry),
		statusLabel,
		kr,
		widget.NewSeparator(),
		widget.NewLabel(lang.X("log_header", "Send log:")),
		logLabel,
	))

	w.ShowAndRun()
}

// --- keyReceiver: クリックでフォーカスを取得しキー入力を転送するウィジェット ---

type keyReceiver struct {
	widget.BaseWidget
	win     fyne.Window
	onKey   func(string, []string)
	focused bool
}

func newKeyReceiver(win fyne.Window, onKey func(string, []string)) *keyReceiver {
	k := &keyReceiver{win: win, onKey: onKey}
	k.ExtendBaseWidget(k)
	return k
}

func (k *keyReceiver) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(color.NRGBA{R: 50, G: 50, B: 70, A: 255})
	text := canvas.NewText(lang.X("key_area_idle", "Click here → Forward key input"), color.White)
	text.Alignment = fyne.TextAlignCenter
	text.TextStyle = fyne.TextStyle{Bold: true}
	obj := container.NewStack(bg, container.NewCenter(text))
	return &keyReceiverRenderer{k: k, bg: bg, text: text, obj: obj}
}

func (k *keyReceiver) MinSize() fyne.Size { return fyne.NewSize(400, 80) }

func (k *keyReceiver) Tapped(_ *fyne.PointEvent) {
	k.win.Canvas().Focus(k)
}

func (k *keyReceiver) FocusGained() { k.focused = true; k.Refresh() }
func (k *keyReceiver) FocusLost()   { k.focused = false; k.Refresh() }

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

func (k *keyReceiver) KeyUp(_ *fyne.KeyEvent) {}

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

// --- renderer ---

type keyReceiverRenderer struct {
	k    *keyReceiver
	bg   *canvas.Rectangle
	text *canvas.Text
	obj  *fyne.Container
}

func (r *keyReceiverRenderer) Layout(size fyne.Size)             { r.obj.Resize(size) }
func (r *keyReceiverRenderer) MinSize() fyne.Size                { return fyne.NewSize(400, 80) }
func (r *keyReceiverRenderer) Destroy()                          {}
func (r *keyReceiverRenderer) Objects() []fyne.CanvasObject      { return []fyne.CanvasObject{r.obj} }

func (r *keyReceiverRenderer) Refresh() {
	if r.k.focused {
		r.bg.FillColor = color.NRGBA{R: 30, G: 100, B: 200, A: 255}
		r.text.Text = lang.X("key_area_active", "Forwarding — Press a key")
	} else {
		r.bg.FillColor = color.NRGBA{R: 50, G: 50, B: 70, A: 255}
		r.text.Text = lang.X("key_area_idle", "Click here → Forward key input")
	}
	r.bg.Refresh()
	r.text.Refresh()
}

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
