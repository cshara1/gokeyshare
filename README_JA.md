# gokeyshare

TCP 経由でキーボード入力をリモートマシンに転送するツールです。
サーバー側で受信したキー入力を robotgo を使ってそのまま再現します。

---

## 構成

| コンポーネント | ディレクトリ | 役割 |
|---|---|---|
| クライアント (GUI) | `gokeyshare/` | キー入力を送信する GUI アプリ |
| サーバー | `gokeyshare-server/` | キー入力を受信・実行するサーバー |

---

## 必要環境

| 項目 | 内容 |
|---|---|
| Go | 1.25 以上 |
| C コンパイラ | robotgo の CGO ビルドに必要 |

### macOS
Xcode Command Line Tools が入っていれば追加インストール不要です。

### Windows
MSYS2 + MinGW-w64 が必要です。

```powershell
# MSYS2 ターミナルで実行
pacman -S mingw-w64-x86_64-gcc
```

インストール後、`C:\msys64\mingw64\bin` を環境変数 PATH に追加してください。

---

## ビルド

```bash
# クライアント (GUI)
go build -o gokeyshare ./gokeyshare/

# サーバー
go build -o gokeyshare-server ./gokeyshare-server/
```

### Windows（CMD ウィンドウなし）

```powershell
# クライアント
GOARCH=amd64 GOOS=windows go build -ldflags "-H windowsgui" -o gokeyshare.exe .\gokeyshare\

# サーバー
GOARCH=amd64 GOOS=windows go build -o gokeyshare-server.exe .\gokeyshare-server\
```

---

## 使い方

### 1. サーバーを起動する（転送先マシン）

```bash
./gokeyshare-server :8080
```

### 2. クライアントを起動する（操作元マシン）

```bash
./gokeyshare
```

GUI が起動します。アドレス欄にサーバーの `ホスト:ポート` を入力して「Connect」ボタンを押し、青いエリアをクリックするとキー入力の転送が始まります。

---

## 環境変数

| 変数名 | 対象 | 説明 |
|---|---|---|
| `VKEYS_SECRET` | 両方 | 共有シークレットによる認証トークン |
| `VKEYS_CERT` | サーバー | TLS 証明書ファイルパス (.pem) |
| `VKEYS_KEY` | サーバー | TLS 秘密鍵ファイルパス (.pem) |
| `VKEYS_TLS` | クライアント | `1` に設定すると TLS 接続を使用 |
| `VKEYS_CA` | クライアント | 検証に使う CA 証明書ファイルパス (.pem) |

### 使用例（認証あり）

```bash
# サーバー
VKEYS_SECRET=mysecret ./gokeyshare-server :8080

# クライアント
VKEYS_SECRET=mysecret ./gokeyshare
```

### 使用例（TLS + 認証あり）

```bash
# サーバー
VKEYS_SECRET=mysecret VKEYS_CERT=cert.pem VKEYS_KEY=key.pem ./gokeyshare-server :8080

# クライアント
VKEYS_SECRET=mysecret VKEYS_TLS=1 VKEYS_CA=cert.pem ./gokeyshare 192.168.1.10:8080
```

---

## 対応キー一覧

### 文字・数字

| 種別 | 内容 |
|---|---|
| 英小文字 | `a` – `z` |
| 英大文字 | `A` – `Z`（Shift として送信） |
| 数字 | `0` – `9` |

### 記号（印字可能 ASCII）

`.` `/` `,` `;` `'` `[` `]` `\` `-` `=` `` ` ``
`!` `@` `#` `$` `%` `^` `&` `*` `(` `)` `+` `_` `|` `~` `"` `<` `>` `?` `{` `}`  など

### 特殊キー

| キー | 送信値 |
|---|---|
| Enter | `enter` |
| Backspace | `backspace` |
| Delete | `delete` |
| Tab | `tab` |
| Escape | `escape` |
| Space | `space` |
| Insert | `insert` |
| Caps Lock | `capslock` |

### ナビゲーション

| キー | 送信値 |
|---|---|
| ↑ ↓ ← → | `up` / `down` / `left` / `right` |
| Home / End | `home` / `end` |
| Page Up / Down | `pageup` / `pagedown` |

### ファンクションキー

`F1` – `F12`

### 修飾キーとの組み合わせ

| 組み合わせ | 例 | 備考 |
|---|---|---|
| Shift + キー | Shift+Enter, Shift+F4 | ✅ |
| Ctrl + キー | Ctrl+C, Ctrl+Z, Ctrl+A | ✅ |
| Alt + 特殊キー | Alt+F4 | ✅ |
| Alt + 文字（Windows） | Alt+A | ✅ |
| Alt + 文字（macOS） | Alt+A | ❌ OS が特殊文字に変換するため未対応 |
| Win/Super キー単体 | Win | ❌ fyne の制限により未対応 |
| IME キー（変換・無変換） | — | ❌ fyne の制限により未対応 |

---

## 多言語対応

OS のロケール設定を自動検出して UI の言語を切り替えます。

| ロケール | 表示言語 |
|---|---|
| `ja` | 日本語 |
| `en`（その他） | 英語 |

翻訳ファイルは `gokeyshare/translations/` に配置されています。
新しい言語を追加する場合は `base.<言語コード>.json` を作成してください。

---

## セキュリティ

- **認証なし・TLS なしの場合**、同一ネットワーク上の誰でも接続してキー操作を実行できます
- 公開ネットワークで使用する場合は必ず `VKEYS_SECRET` と TLS を設定してください
- メッセージサイズの上限は 1MB です
