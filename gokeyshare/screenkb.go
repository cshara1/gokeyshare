package main

import (
	"image/color"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// --- Screen keyboard constants (natural/minimum sizes) ---

const (
	skUnit    float32 = 27 // base unit width for key sizing
	skKeyH    float32 = 26 // base key height
	skGap     float32 = 1  // base gap between keys
	skRowUnits float32 = 15 // widest row in units (standard keyboard)
	skNumRows  float32 = 8  // number of keyboard rows
)

var (
	skColorNormal     = color.NRGBA{R: 55, G: 55, B: 60, A: 255}
	skColorPressed    = color.NRGBA{R: 30, G: 100, B: 200, A: 255}
	skColorSupplement = color.NRGBA{R: 55, G: 65, B: 70, A: 255}
	skColorText       = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
)

// --- Key definition ---

type skKeyDef struct {
	Label      string
	ID         string
	W          float32
	Supplement bool
}

func skK(label, id string, w float32) skKeyDef {
	return skKeyDef{Label: label, ID: id, W: w}
}

func skSup(label, id string, w float32) skKeyDef {
	return skKeyDef{Label: label, ID: id, W: w, Supplement: true}
}

func skSpacer(w float32) skKeyDef {
	return skKeyDef{W: w}
}

// --- Common rows (shared across all layouts) ---

var (
	skFnRow = []skKeyDef{
		skK("Esc", "escape", 1.5), skSpacer(0.5),
		skK("F1", "f1", 1), skK("F2", "f2", 1), skK("F3", "f3", 1), skK("F4", "f4", 1), skSpacer(0.5),
		skK("F5", "f5", 1), skK("F6", "f6", 1), skK("F7", "f7", 1), skK("F8", "f8", 1), skSpacer(0.5),
		skK("F9", "f9", 1), skK("F10", "f10", 1), skK("F11", "f11", 1), skK("F12", "f12", 1),
	}
	skQwertyRow = []skKeyDef{
		skK("Tab", "tab", 1.5),
		skK("Q", "q", 1), skK("W", "w", 1), skK("E", "e", 1), skK("R", "r", 1), skK("T", "t", 1),
		skK("Y", "y", 1), skK("U", "u", 1), skK("I", "i", 1), skK("O", "o", 1), skK("P", "p", 1),
		skK("[", "[", 1), skK("]", "]", 1),
		skK("\\", "\\", 1.5),
	}
	skHomeRow = []skKeyDef{
		skSup("Caps", "capslock", 1.75),
		skK("A", "a", 1), skK("S", "s", 1), skK("D", "d", 1), skK("F", "f", 1), skK("G", "g", 1),
		skK("H", "h", 1), skK("J", "j", 1), skK("K", "k", 1), skK("L", "l", 1),
		skK(";", ";", 1), skK("'", "'", 1),
		skK("↵", "enter", 2.25),
	}
	skShiftRow = []skKeyDef{
		skK("Shift", "lshift", 2.25),
		skK("Z", "z", 1), skK("X", "x", 1), skK("C", "c", 1), skK("V", "v", 1), skK("B", "b", 1),
		skK("N", "n", 1), skK("M", "m", 1),
		skK(",", ",", 1), skK(".", ".", 1), skK("/", "/", 1),
		skK("Shift", "rshift", 2.75),
	}
	// 矢印行は同じアイテム数・同じ合計幅にして ↑ と ↓ の X 位置を揃える
	skArrowUpRow = []skKeyDef{skSpacer(11), skSpacer(1), skK("↑", "up", 1), skSpacer(1)}
	skArrowLRRow = []skKeyDef{skSpacer(11), skK("←", "left", 1), skK("↓", "down", 1), skK("→", "right", 1)}
)

// --- Layout definitions ---

type skLayoutDef struct {
	ID      string
	Name    string
	NumRow  []skKeyDef
	Bottom  []skKeyDef
	Aliases map[string]string // fyneNameToScreenID の結果 → 実際の画面キーID
}

// Number rows
var (
	skNumRowUS = []skKeyDef{
		skK("`", "`", 1),
		skK("1", "1", 1), skK("2", "2", 1), skK("3", "3", 1), skK("4", "4", 1), skK("5", "5", 1),
		skK("6", "6", 1), skK("7", "7", 1), skK("8", "8", 1), skK("9", "9", 1), skK("0", "0", 1),
		skK("-", "-", 1), skK("=", "=", 1),
		skK("⌫", "backspace", 2),
	}
	skNumRowJISWin = []skKeyDef{
		skSup("半/全", "hankaku", 1),
		skK("1", "1", 1), skK("2", "2", 1), skK("3", "3", 1), skK("4", "4", 1), skK("5", "5", 1),
		skK("6", "6", 1), skK("7", "7", 1), skK("8", "8", 1), skK("9", "9", 1), skK("0", "0", 1),
		skK("-", "-", 1), skK("=", "=", 1),
		skK("⌫", "backspace", 2),
	}
)

// All supported layouts
var skLayoutDefs = []skLayoutDef{
	{
		ID:     "us",
		Name:   "US (ANSI)",
		NumRow: skNumRowUS,
		Bottom: []skKeyDef{
			skK("Ctrl", "lctrl", 1.5), skSup("Super", "lsuper", 1.25), skK("Alt", "lalt", 1.25),
			skK("", "space", 7),
			skK("Alt", "ralt", 1.25), skSup("Super", "rsuper", 1.25), skK("Ctrl", "rctrl", 1.5),
		},
	},
	{
		ID:     "jis_win",
		Name:   "JIS (Windows)",
		NumRow: skNumRowJISWin,
		Bottom: []skKeyDef{
			skK("Ctrl", "lctrl", 1.25), skSup("Win", "lwin", 1), skK("Alt", "lalt", 1),
			skSup("無変", "muhenkan", 1.25), skK("", "space", 5), skSup("変換", "henkan", 1.25),
			skSup("カナ", "kana", 1), skK("Alt", "ralt", 1), skSup("Win", "rwin", 1),
			skK("Ctrl", "rctrl", 1.25),
		},
		Aliases: map[string]string{
			"`":      "hankaku",
			"lsuper": "lwin",
			"rsuper": "rwin",
		},
	},
	{
		ID:     "mac_us",
		Name:   "Mac (US)",
		NumRow: skNumRowUS,
		Bottom: []skKeyDef{
			skSup("fn", "fn", 1), skK("Ctrl", "lctrl", 1.25), skK("Opt", "lalt", 1.25),
			skK("⌘", "lcmd", 1.5), skK("", "space", 7.25),
			skK("⌘", "rcmd", 1.5), skK("Opt", "ralt", 1.25),
		},
		Aliases: map[string]string{
			"lsuper": "lcmd",
			"rsuper": "rcmd",
		},
	},
	{
		ID:     "mac_jis",
		Name:   "Mac (JIS)",
		NumRow: skNumRowUS,
		Bottom: []skKeyDef{
			skSup("fn", "fn", 1), skK("Ctrl", "lctrl", 1.25), skK("Opt", "lalt", 1.25),
			skK("⌘", "lcmd", 1.5), skSup("英数", "eisu", 1.25), skK("", "space", 5),
			skSup("かな", "kana_mac", 1.25), skK("⌘", "rcmd", 1.5), skK("Opt", "ralt", 1),
		},
		Aliases: map[string]string{
			"lsuper": "lcmd",
			"rsuper": "rcmd",
		},
	},
}

// --- Platform → available layouts mapping ---

var skPlatformLayouts = map[string][]string{
	"windows": {"us", "jis_win"},
	"darwin":  {"mac_us", "mac_jis"},
	"linux":   {"us", "jis_win", "mac_jis"},
}

func skLayoutNamesForPlatform(platform string) []string {
	ids, ok := skPlatformLayouts[platform]
	if !ok || len(ids) == 0 {
		return skLayoutNames()
	}
	var names []string
	for _, id := range ids {
		for _, l := range skLayoutDefs {
			if l.ID == id {
				names = append(names, l.Name)
				break
			}
		}
	}
	return names
}

// --- Layout helpers ---

func skLayoutNames() []string {
	names := make([]string, len(skLayoutDefs))
	for i, l := range skLayoutDefs {
		names[i] = l.Name
	}
	return names
}

func skLayoutIDByName(name string) string {
	for _, l := range skLayoutDefs {
		if l.Name == name {
			return l.ID
		}
	}
	return skLayoutDefs[0].ID
}

func skLayoutNameByID(id string) string {
	for _, l := range skLayoutDefs {
		if l.ID == id {
			return l.Name
		}
	}
	return skLayoutDefs[0].Name
}

func skFindLayout(id string) *skLayoutDef {
	for i := range skLayoutDefs {
		if skLayoutDefs[i].ID == id {
			return &skLayoutDefs[i]
		}
	}
	return &skLayoutDefs[0]
}

func skBuildAllRows(layout *skLayoutDef) [][]skKeyDef {
	return [][]skKeyDef{
		skFnRow,
		layout.NumRow,
		skQwertyRow,
		skHomeRow,
		skShiftRow,
		layout.Bottom,
		skArrowUpRow,
		skArrowLRRow,
	}
}

// --- screenKeyboard ---

var skColorBg = color.NRGBA{R: 230, G: 230, B: 233, A: 255}

type screenKeyboard struct {
	Container    *fyne.Container // Stack(background, keyboard rows)
	kbRows       *fyne.Container // keyboard rows with skBoardLayout
	keys         map[string]*skKey
	mu           sync.Mutex
	onSupplement func(string, []string)
	refocus      func()
}

func newScreenKeyboard(layoutID string, onSupplement func(string, []string), refocus func()) *screenKeyboard {
	bg := canvas.NewRectangle(skColorBg)
	bg.CornerRadius = 6
	rows := container.New(&skBoardLayout{})
	sk := &screenKeyboard{
		keys:         make(map[string]*skKey),
		onSupplement: onSupplement,
		refocus:      refocus,
		kbRows:       rows,
		Container:    container.NewStack(bg, rows),
	}
	sk.SetLayout(layoutID)
	return sk
}

func (sk *screenKeyboard) SetLayout(layoutID string) {
	layout := skFindLayout(layoutID)

	sk.mu.Lock()
	defer sk.mu.Unlock()

	sk.keys = make(map[string]*skKey)

	allRows := skBuildAllRows(layout)
	var rowObjects []fyne.CanvasObject
	for _, rowDef := range allRows {
		rowObjects = append(rowObjects, sk.buildRow(rowDef))
	}

	// Register aliases so physical key presses highlight the correct screen key
	for physID, screenID := range layout.Aliases {
		if kw, ok := sk.keys[screenID]; ok {
			sk.keys[physID] = kw
		}
	}

	sk.kbRows.Objects = rowObjects
	sk.kbRows.Refresh()
}

func (sk *screenKeyboard) buildRow(rowDef []skKeyDef) fyne.CanvasObject {
	var items []fyne.CanvasObject
	for _, d := range rowDef {
		if d.ID == "" && d.Label == "" {
			items = append(items, newSKSpacerWidget(d.W))
			continue
		}
		kw := newSKKey(d)
		if d.Supplement {
			localID := d.ID
			kw.onTap = func() {
				if sk.onSupplement != nil {
					sk.onSupplement(localID, nil)
				}
				if sk.refocus != nil {
					sk.refocus()
				}
			}
		} else {
			kw.onTap = func() {
				if sk.refocus != nil {
					sk.refocus()
				}
			}
		}
		sk.keys[d.ID] = kw
		items = append(items, kw)
	}
	return container.New(&skRowLayout{gap: skGap}, items...)
}

func (sk *screenKeyboard) ClearAllPressed() {
	sk.mu.Lock()
	defer sk.mu.Unlock()
	for _, kw := range sk.keys {
		kw.setPressed(false)
	}
}

func (sk *screenKeyboard) SetPressed(id string, pressed bool) {
	sk.mu.Lock()
	defer sk.mu.Unlock()
	if kw, ok := sk.keys[id]; ok {
		kw.setPressed(pressed)
	}
}

// --- Individual key widget ---

type skKey struct {
	widget.BaseWidget
	def     skKeyDef
	pressed bool
	onTap   func()
}

func newSKKey(d skKeyDef) *skKey {
	kw := &skKey{def: d}
	kw.ExtendBaseWidget(kw)
	return kw
}

func (k *skKey) setPressed(p bool) {
	if k.pressed != p {
		k.pressed = p
		k.Refresh()
	}
}

func (k *skKey) MinSize() fyne.Size {
	return fyne.NewSize(skUnit*k.def.W, skKeyH)
}

func (k *skKey) Tapped(_ *fyne.PointEvent) {
	if k.onTap != nil {
		k.onTap()
	}
}

func (k *skKey) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(skColorNormal)
	bg.CornerRadius = 3
	label := canvas.NewText(k.def.Label, skColorText)
	label.TextSize = 10
	label.Alignment = fyne.TextAlignCenter
	c := container.NewStack(bg, container.NewCenter(label))
	return &skKeyRenderer{k: k, bg: bg, label: label, container: c}
}

type skKeyRenderer struct {
	k         *skKey
	bg        *canvas.Rectangle
	label     *canvas.Text
	container *fyne.Container
}

func (r *skKeyRenderer) Layout(size fyne.Size) {
	r.container.Resize(size)
	// Scale font with key height
	r.label.TextSize = 10 * (size.Height / skKeyH)
	r.bg.CornerRadius = 3 * (size.Height / skKeyH)
}
func (r *skKeyRenderer) MinSize() fyne.Size            { return r.k.MinSize() }
func (r *skKeyRenderer) Destroy()                      {}
func (r *skKeyRenderer) Objects() []fyne.CanvasObject  { return []fyne.CanvasObject{r.container} }

func (r *skKeyRenderer) Refresh() {
	if r.k.pressed {
		r.bg.FillColor = skColorPressed
	} else if r.k.def.Supplement {
		r.bg.FillColor = skColorSupplement
	} else {
		r.bg.FillColor = skColorNormal
	}
	r.bg.Refresh()
	r.label.Refresh()
}

// --- Spacer widget ---

type skSpacerWidget struct {
	widget.BaseWidget
	w float32
}

func newSKSpacerWidget(w float32) *skSpacerWidget {
	s := &skSpacerWidget{w: w}
	s.ExtendBaseWidget(s)
	return s
}

func (s *skSpacerWidget) MinSize() fyne.Size {
	return fyne.NewSize(skUnit*s.w, skKeyH)
}

func (s *skSpacerWidget) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}

// --- Row layout (scales keys proportionally to fill available width) ---

type skRowLayout struct {
	gap float32
}

func (l *skRowLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	var w, h float32
	for i, o := range objects {
		ms := o.MinSize()
		w += ms.Width
		if i > 0 {
			w += l.gap
		}
		if ms.Height > h {
			h = ms.Height
		}
	}
	return fyne.NewSize(w, h)
}

func (l *skRowLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	minSize := l.MinSize(objects)
	if minSize.Width <= 0 {
		return
	}
	scale := size.Width / minSize.Width

	x := float32(0)
	for i, o := range objects {
		if i > 0 {
			x += l.gap * scale
		}
		ms := o.MinSize()
		w := ms.Width * scale
		o.Resize(fyne.NewSize(w, size.Height))
		o.Move(fyne.NewPos(x, 0))
		x += w
	}
}

// --- Board layout (stacks rows vertically, maintaining keyboard aspect ratio) ---

type skBoardLayout struct{}

func (l *skBoardLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	// Natural size: widest row width × total row heights
	var maxW float32
	var totalH float32
	for i, o := range objects {
		ms := o.MinSize()
		if ms.Width > maxW {
			maxW = ms.Width
		}
		totalH += ms.Height
		if i > 0 {
			totalH += skGap
		}
	}
	return fyne.NewSize(maxW, totalH)
}

func (l *skBoardLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	minSize := l.MinSize(objects)
	if minSize.Width <= 0 || minSize.Height <= 0 {
		return
	}

	// Scale uniformly based on width to maintain aspect ratio
	scale := size.Width / minSize.Width
	scaledH := minSize.Height * scale

	// If scaled height exceeds available height, shrink by height instead
	if scaledH > size.Height {
		scale = size.Height / minSize.Height
	}

	// Center vertically if there's extra space
	totalH := float32(0)
	for i, o := range objects {
		totalH += o.MinSize().Height * scale
		if i > 0 {
			totalH += skGap * scale
		}
	}
	y := (size.Height - totalH) / 2
	if y < 0 {
		y = 0
	}

	for i, o := range objects {
		if i > 0 {
			y += skGap * scale
		}
		ms := o.MinSize()
		rowH := ms.Height * scale
		o.Resize(fyne.NewSize(size.Width, rowH))
		o.Move(fyne.NewPos(0, y))
		y += rowH
	}
}

// --- fyne.KeyName → screen key ID mapping ---
// Returns fixed IDs; per-layout aliases in screenKeyboard handle the rest.

func fyneNameToScreenID(name fyne.KeyName) string {
	s := string(name)
	if len(s) == 1 {
		c := s[0]
		if c >= 'A' && c <= 'Z' {
			return string(rune(c + 32))
		}
		return s
	}
	switch name {
	case fyne.KeyReturn:
		return "enter"
	case fyne.KeyBackspace:
		return "backspace"
	case fyne.KeyDelete:
		return "delete"
	case fyne.KeyEscape:
		return "escape"
	case fyne.KeyTab:
		return "tab"
	case fyne.KeySpace:
		return "space"
	case fyne.KeyUp:
		return "up"
	case fyne.KeyDown:
		return "down"
	case fyne.KeyLeft:
		return "left"
	case fyne.KeyRight:
		return "right"
	case fyne.KeyHome:
		return "home"
	case fyne.KeyEnd:
		return "end"
	case fyne.KeyPageUp:
		return "pageup"
	case fyne.KeyPageDown:
		return "pagedown"
	case fyne.KeyInsert:
		return "insert"
	case fyne.KeyF1:
		return "f1"
	case fyne.KeyF2:
		return "f2"
	case fyne.KeyF3:
		return "f3"
	case fyne.KeyF4:
		return "f4"
	case fyne.KeyF5:
		return "f5"
	case fyne.KeyF6:
		return "f6"
	case fyne.KeyF7:
		return "f7"
	case fyne.KeyF8:
		return "f8"
	case fyne.KeyF9:
		return "f9"
	case fyne.KeyF10:
		return "f10"
	case fyne.KeyF11:
		return "f11"
	case fyne.KeyF12:
		return "f12"
	}
	// Modifier and special keys by string value
	switch s {
	case "LeftShift":
		return "lshift"
	case "RightShift":
		return "rshift"
	case "LeftControl":
		return "lctrl"
	case "RightControl":
		return "rctrl"
	case "LeftAlt":
		return "lalt"
	case "RightAlt":
		return "ralt"
	case "LeftSuper":
		return "lsuper"
	case "RightSuper":
		return "rsuper"
	case "CapsLock":
		return "capslock"
	}
	return ""
}
