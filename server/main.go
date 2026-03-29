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
	"time"

	"github.com/go-vgo/robotgo"
)

const (
	maxMsgSize  = 1 * 1024 * 1024 // 1MB
	readTimeout = 60 * time.Second
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
	// 単一英数字、ファンクションキー(f1-f12)、特殊キーのみ許可
	validKeyRe = regexp.MustCompile(`^[a-z0-9]$|^f(1[0-2]|[1-9])$|^(space|enter|backspace|tab|escape|delete|home|end|pageup|pagedown|up|down|left|right|insert|capslock)$`)
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

	if secret != "" {
		if err := authenticate(conn, secret); err != nil {
			log.Printf("認証失敗 (%s): %v", conn.RemoteAddr(), err)
			return
		}
	}

	sizeBuf := make([]byte, 4) // ループ外で確保して再利用
	for {
		conn.SetReadDeadline(time.Now().Add(readTimeout))

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

		dispatchEvent(msgBuf)
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

func dispatchEvent(msgBuf []byte) {
	event := InputShare.GetRootAsEvent(msgBuf, 0)
	keyEvent := new(InputShare.KeyEvent)
	event.KeyEvent(keyEvent)

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
	robotgo.KeyTap(mainKey, args...)
}
