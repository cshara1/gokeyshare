# ビルド手順

## 構成

| ディレクトリ | 役割 | 依存 |
|---|---|---|
| `gokeyshare/` | クライアント (GUI) | Fyne, CGO |
| `gokeyshare-server/` | サーバー (キー入力再生) | robotgo, CGO |
| `InputShare/` | FlatBuffers 生成コード | — |

> **注意:** サーバー・クライアントともに CGO を使用するため、C コンパイラが必須です。

---

## Windows

### 必要なツール

#### 1. Go
[https://go.dev/dl/](https://go.dev/dl/) から最新の `.msi` をダウンロードしてインストール。

```powershell
go version
```

#### 2. C コンパイラ (MinGW-w64 via MSYS2)

1. [https://www.msys2.org/](https://www.msys2.org/) から MSYS2 をインストール（デフォルト: `C:\msys64`）
2. MSYS2 ターミナルを開いて実行:

```bash
pacman -S mingw-w64-x86_64-gcc mingw-w64-x86_64-mpc mingw-w64-x86_64-mpfr
```

3. `C:\msys64\mingw64\bin` をシステム PATH に追加するか、ビルド時に指定する

### ビルド

```powershell
cd C:\path\to\gokeyshare

# 依存パッケージの取得
go mod download

# サーバー
$env:PATH = $env:PATH + ";C:\msys64\mingw64\bin"
$env:CC = "C:\msys64\mingw64\bin\gcc.exe"
go build -o server.exe .\gokeyshare-server\

# クライアント (GUI)
go build -o client.exe .\gokeyshare\
```

### 実行

```powershell
# サーバー（ポート 8080 で起動）
.\server.exe

# サーバー（ポートとシークレット指定）
$env:VKEYS_SECRET = "mysecret"
.\server.exe :8080

# クライアント (GUI)
.\client.exe
```

---

## macOS

### 必要なツール

- Go: [https://go.dev/dl/](https://go.dev/dl/)
- Xcode Command Line Tools: `xcode-select --install`

### ビルド

```bash
cd /path/to/gokeyshare
go mod download
go build -o server ./gokeyshare-server/
go build -o client ./gokeyshare/
```

### 実行

```bash
./server          # :8080 で起動
./client          # GUI クライアント
```

---

## Linux

### 必要なツール

```bash
# Ubuntu / Debian
sudo apt-get install -y gcc libgl1-mesa-dev xorg-dev libxtst-dev libpng-dev

# Fedora / RHEL
sudo dnf install -y gcc mesa-libGL-devel libX11-devel libXtst-devel
```

### ビルド

```bash
cd /path/to/gokeyshare
go mod download
go build -o server ./gokeyshare-server/
go build -o client ./gokeyshare/
```

---

## 環境変数

| 変数 | 対象 | 説明 |
|---|---|---|
| `VKEYS_SECRET` | サーバー / クライアント | 認証トークン（省略時は認証なし） |
| `VKEYS_CERT` | サーバー | TLS 証明書ファイルパス |
| `VKEYS_KEY` | サーバー | TLS 秘密鍵ファイルパス |
| `VKEYS_TLS` | クライアント | `1` にすると TLS 接続を使用 |
| `VKEYS_CA` | クライアント | TLS CA 証明書ファイルパス（省略時は証明書検証をスキップ） |

---

## TLS を使う場合

```bash
# 自己署名証明書の生成例
openssl req -x509 -newkey rsa:4096 -keyout key.pem -out cert.pem -days 365 -nodes

# サーバー
VKEYS_CERT=cert.pem VKEYS_KEY=key.pem ./server :8080

# クライアント（CA 証明書を指定）
VKEYS_TLS=1 VKEYS_CA=cert.pem ./client
```

---

## GitHub Actions でのビルド

`.github/workflows/build.yml` を参照してください。
push / PR 時に Windows・macOS・Linux の 3 プラットフォームで自動ビルドされます。
