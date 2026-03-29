package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"gokeyshare/InputShare"
	"log"
	"net"
	"os"
	"time"

	flatbuffers "github.com/google/flatbuffers/go"
)

const writeTimeout = 10 * time.Second

// 送信するキーイベントの定義
type KeyEventToSend struct {
	Key       string
	Modifiers []string
}

func main() {
	addr := "localhost:8080"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}

	secret := os.Getenv("VKEYS_SECRET")
	useTLS := os.Getenv("VKEYS_TLS") == "1"
	caFile := os.Getenv("VKEYS_CA")

	conn, err := dial(addr, useTLS, caFile)
	if err != nil {
		log.Fatalf("接続エラー: %v", err)
	}
	defer conn.Close()

	// 認証トークンを送信
	if secret != "" {
		conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		if _, err := conn.Write([]byte(secret)); err != nil {
			log.Fatalf("認証トークン送信エラー: %v", err)
		}
		conn.SetWriteDeadline(time.Time{})
	}

	builder := flatbuffers.NewBuilder(1024)

	// --- 送信するキー入力のシミュレーション ---
	events := []KeyEventToSend{
		{Key: "a", Modifiers: []string{}},       // 'a'を単体で送信
		{Key: "f4", Modifiers: []string{"alt"}}, // "Alt + F4" を送信
	}

	for _, event := range events {
		log.Printf("送信: Key=%q, Modifiers=%v", event.Key, event.Modifiers)

		buf := createKeyEvent(builder, InputShare.EventTypeKeyDown, event)
		if err := sendBuffer(conn, buf); err != nil {
			log.Fatalf("送信エラー: %v", err)
		}

		time.Sleep(2 * time.Second)
	}
	log.Println("送信完了")
}

func dial(addr string, useTLS bool, caFile string) (net.Conn, error) {
	if !useTLS {
		return net.Dial("tcp", addr)
	}

	tlsCfg := &tls.Config{}
	if caFile != "" {
		certPEM, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("CA証明書の読み込みエラー: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(certPEM) {
			return nil, fmt.Errorf("CA証明書の解析に失敗しました")
		}
		tlsCfg.RootCAs = pool
	} else {
		tlsCfg.InsecureSkipVerify = true
		log.Println("警告: TLS証明書の検証をスキップします (VKEYS_CA で CA 証明書を指定してください)")
	}

	return tls.Dial("tcp", addr, tlsCfg)
}

func sendBuffer(conn net.Conn, buf []byte) error {
	sizeBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(sizeBuf, uint32(len(buf)))

	conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	defer conn.SetWriteDeadline(time.Time{})

	if _, err := conn.Write(sizeBuf); err != nil {
		return fmt.Errorf("サイズ送信エラー: %w", err)
	}
	if _, err := conn.Write(buf); err != nil {
		return fmt.Errorf("データ送信エラー: %w", err)
	}
	return nil
}

// FlatBuffersのバイナリを作成する関数 (修飾キー対応)
func createKeyEvent(builder *flatbuffers.Builder, eventType InputShare.EventType, eventToSend KeyEventToSend) []byte {
	builder.Reset()

	// 1. 主キーと修飾キーの文字列データを作成
	keyStr := builder.CreateString(eventToSend.Key)

	modOffsets := make([]flatbuffers.UOffsetT, len(eventToSend.Modifiers))
	for i, mod := range eventToSend.Modifiers {
		modOffsets[i] = builder.CreateString(mod)
	}

	// 2. 修飾キーのリスト(Vector)を作成
	InputShare.KeyEventStartModifiersVector(builder, len(modOffsets))
	for i := len(modOffsets) - 1; i >= 0; i-- {
		builder.PrependUOffsetT(modOffsets[i])
	}
	modsVec := builder.EndVector(len(modOffsets))

	// 3. KeyEventテーブルを作成
	InputShare.KeyEventStart(builder)
	InputShare.KeyEventAddKey(builder, keyStr)
	InputShare.KeyEventAddModifiers(builder, modsVec)
	keyEvent := InputShare.KeyEventEnd(builder)

	// 4. ルートとなるEventテーブルを作成
	InputShare.EventStart(builder)
	InputShare.EventAddEventType(builder, eventType)
	InputShare.EventAddKeyEvent(builder, keyEvent)
	event := InputShare.EventEnd(builder)

	builder.Finish(event)
	return builder.FinishedBytes()
}
