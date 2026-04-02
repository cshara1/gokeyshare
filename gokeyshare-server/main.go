package main

import (
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"gokeyshare/InputShare"
	"log"
	"net"
	"os"
	"regexp"
	"runtime"
	"time"

	flatbuffers "github.com/google/flatbuffers/go"
	"github.com/go-vgo/robotgo"
)

const (
	maxMsgSize  = 1 * 1024 * 1024 // 1MB
	authTimeout = 5 * time.Second
)

var (
	validModifiers = map[string]bool{
		"shift": true, "ctrl": true, "alt": true,
		"cmd": true, "super": true,
		"lshift": true, "rshift": true,
		"lctrl": true, "rctrl": true,
		"lalt": true, "ralt": true,
	}
	// 印字可能ASCII単一文字、ファンクションキー(f1-f12)、特殊キーのみ許可
	validKeyRe = regexp.MustCompile(`^[!-~]$|^f(1[0-2]|[1-9])$|^(space|enter|backspace|tab|escape|delete|home|end|pageup|pagedown|up|down|left|right|insert|capslock|alt|shift|ctrl|lwin|rwin|lsuper|rsuper|lcmd|rcmd|fn|hankaku|muhenkan|henkan|kana|eisu|kana_mac)$`)
)

func main() {
	addr := ":8080"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}

	secret := os.Getenv("VKEYS_SECRET")
	certFile := os.Getenv("VKEYS_CERT")
	keyFile := os.Getenv("VKEYS_KEY")

	var listener net.Listener
	var err error

	if certFile != "" && keyFile != "" {
		cert, certErr := tls.LoadX509KeyPair(certFile, keyFile)
		if certErr != nil {
			log.Fatalf("証明書の読み込みエラー: %v", certErr)
		}
		tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}}
		listener, err = tls.Listen("tcp", addr, tlsCfg)
		log.Printf("TLSサーバーを起動します (アドレス: %s)", addr)
	} else {
		listener, err = net.Listen("tcp", addr)
		log.Printf("サーバーを起動します (アドレス: %s)", addr)
	}

	if err != nil {
		log.Fatalf("リッスンエラー: %v", err)
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Println("Acceptエラー:", err)
			continue
		}
		go handleConnection(conn, secret)
	}
}

func handleConnection(conn net.Conn, secret string) {
	log.Printf("クライアントが接続しました: %s", conn.RemoteAddr())
	defer conn.Close()

	// TCP keepalive を有効化して dead connection を検出する
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(30 * time.Second)
	}

	if secret != "" {
		if err := authenticate(conn, secret); err != nil {
			log.Printf("認証失敗 (%s): %v", conn.RemoteAddr(), err)
			return
		}
	}

	sizeBuf := make([]byte, 4) // ループ外で確保して再利用
	for {

		if _, err := io.ReadFull(conn, sizeBuf); err != nil {
			if err == io.EOF {
				log.Println("クライアントが切断しました")
			} else {
				log.Println("サイズ読み取りエラー:", err)
			}
			return
		}

		msgSize := binary.LittleEndian.Uint32(sizeBuf)
		if msgSize == 0 || msgSize > maxMsgSize {
			log.Printf("不正なメッセージサイズ: %d バイト", msgSize)
			return
		}

		msgBuf := make([]byte, msgSize)
		if _, err := io.ReadFull(conn, msgBuf); err != nil {
			log.Println("メッセージ読み取りエラー:", err)
			return
		}

		dispatchEvent(msgBuf, conn)
	}
}

func authenticate(conn net.Conn, secret string) error {
	conn.SetDeadline(time.Now().Add(authTimeout))
	defer conn.SetDeadline(time.Time{})

	buf := make([]byte, len(secret))
	if _, err := io.ReadFull(conn, buf); err != nil {
		return err
	}
	if string(buf) != secret {
		return errors.New("トークン不一致")
	}
	return nil
}

func dispatchEvent(msgBuf []byte, conn net.Conn) {
	event := InputShare.GetRootAsEvent(msgBuf, 0)

	if event.EventType() == InputShare.EventTypePlatformQuery {
		sendPlatformInfo(conn)
		return
	}

	keyEvent := new(InputShare.KeyEvent)
	event.KeyEvent(keyEvent)

	// クリップボードイベント: key フィールドにテキストが入っている
	if event.EventType() == InputShare.EventTypeClipboard {
		text := string(keyEvent.Key())
		if err := robotgo.WriteAll(text); err != nil {
			log.Printf("クリップボード書き込みエラー: %v", err)
			return
		}
		robotgo.KeyTap("v", "ctrl")
		log.Printf("クリップボード貼り付け: %d 文字", len([]rune(text)))
		return
	}

	mainKey := string(keyEvent.Key())
	if !validKeyRe.MatchString(mainKey) {
		log.Printf("不正なキー名: %q", mainKey)
		return
	}

	modifiers := make([]string, keyEvent.ModifiersLength())
	for i := range modifiers {
		mod := string(keyEvent.Modifiers(i))
		if !validModifiers[mod] {
			log.Printf("不正な修飾キー: %q", mod)
			return
		}
		modifiers[i] = mod
	}

	log.Printf("受信: Key=%q, Modifiers=%v", mainKey, modifiers)

	args := make([]interface{}, len(modifiers))
	for i, v := range modifiers {
		args[i] = v
	}

	// 特殊キーのリマッピング
	switch mainKey {
	case "lwin", "lsuper", "lcmd":
		robotgo.KeyTap("cmd", args...)
		return
	case "rwin", "rsuper", "rcmd":
		robotgo.KeyTap("rcmd", args...)
		return
	case "hankaku":
		// 半角/全角: Alt+` で IME 切り替え
		robotgo.KeyTap("`", "alt")
		return
	case "fn", "muhenkan", "henkan", "kana", "eisu", "kana_mac":
		if !platformKeyTap(mainKey) {
			log.Printf("  → 未対応キー: %q (このプラットフォームでは非対応)", mainKey)
		}
		return
	}

	robotgo.KeyTap(mainKey, args...)
}

func sendPlatformInfo(conn net.Conn) {
	b := flatbuffers.NewBuilder(64)

	platform := b.CreateString(runtime.GOOS)
	InputShare.KeyEventStartModifiersVector(b, 0)
	modsVec := b.EndVector(0)
	InputShare.KeyEventStart(b)
	InputShare.KeyEventAddKey(b, platform)
	InputShare.KeyEventAddModifiers(b, modsVec)
	ke := InputShare.KeyEventEnd(b)

	InputShare.EventStart(b)
	InputShare.EventAddEventType(b, InputShare.EventTypePlatformInfo)
	InputShare.EventAddKeyEvent(b, ke)
	ev := InputShare.EventEnd(b)
	b.Finish(ev)

	buf := b.FinishedBytes()
	sizeBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(sizeBuf, uint32(len(buf)))

	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	conn.Write(sizeBuf)
	conn.Write(buf)
	conn.SetWriteDeadline(time.Time{})

	log.Printf("プラットフォーム情報を送信: %s", runtime.GOOS)
}
