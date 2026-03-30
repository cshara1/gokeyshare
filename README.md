# gokeyshare

A tool for forwarding keyboard input to a remote machine over TCP.
The server receives key events and reproduces them locally using robotgo.

> Japanese documentation: [README_JA.md](README_JA.md)

---

## Components

| Component | Directory | Role |
|---|---|---|
| Client (GUI) | `gokeyshare/` | GUI app that captures and sends key input |
| Server | `gokeyshare-server/` | Receives and executes key input |

---

## Requirements

| Item | Details |
|---|---|
| Go | 1.25 or later |
| C compiler | Required for robotgo's CGO build |

### macOS
No additional setup needed if Xcode Command Line Tools are installed.

### Windows
MSYS2 + MinGW-w64 is required.

```powershell
# Run in MSYS2 terminal
pacman -S mingw-w64-x86_64-gcc
```

Add `C:\msys64\mingw64\bin` to your system PATH environment variable.

---

## Build

```bash
# Client (GUI)
go build -o gokeyshare ./gokeyshare/

# Server
go build -o gokeyshare-server ./gokeyshare-server/
```

### Windows (no CMD window)

```powershell
# Client
GOARCH=amd64 GOOS=windows go build -ldflags "-H windowsgui" -o gokeyshare.exe .\gokeyshare\

# Server
GOARCH=amd64 GOOS=windows go build -o gokeyshare-server.exe .\gokeyshare-server\
```

---

## Usage

### 1. Start the server (target machine)

```bash
./gokeyshare-server :8080
```

### 2. Start the client (source machine)

```bash
./gokeyshare
```

The GUI will open. Enter the server's `host:port` in the address field, click **Connect**, then click the blue area to start forwarding key input.

Previously used addresses are saved and available from the dropdown.

---

## Environment Variables

| Variable | Side | Description |
|---|---|---|
| `VKEYS_SECRET` | Both | Shared secret for authentication |
| `VKEYS_CERT` | Server | TLS certificate file path (.pem) |
| `VKEYS_KEY` | Server | TLS private key file path (.pem) |
| `VKEYS_TLS` | Client | Set to `1` to enable TLS connection |
| `VKEYS_CA` | Client | CA certificate file path for verification (.pem) |

### Example (with authentication)

```bash
# Server
VKEYS_SECRET=mysecret ./gokeyshare-server :8080

# Client
VKEYS_SECRET=mysecret ./gokeyshare
```

### Example (TLS + authentication)

```bash
# Server
VKEYS_SECRET=mysecret VKEYS_CERT=cert.pem VKEYS_KEY=key.pem ./gokeyshare-server :8080

# Client
VKEYS_SECRET=mysecret VKEYS_TLS=1 VKEYS_CA=cert.pem ./gokeyshare 192.168.1.10:8080
```

---

## Supported Keys

### Letters & Digits

| Type | Keys |
|---|---|
| Lowercase | `a` – `z` |
| Uppercase | `A` – `Z` (sent as Shift + lowercase) |
| Digits | `0` – `9` |

### Symbols (printable ASCII)

`.` `/` `,` `;` `'` `[` `]` `\` `-` `=` `` ` ``
`!` `@` `#` `$` `%` `^` `&` `*` `(` `)` `+` `_` `|` `~` `"` `<` `>` `?` `{` `}` etc.

### Special Keys

| Key | Sent as |
|---|---|
| Enter | `enter` |
| Backspace | `backspace` |
| Delete | `delete` |
| Tab | `tab` |
| Escape | `escape` |
| Space | `space` |
| Insert | `insert` |
| Caps Lock | `capslock` |

### Navigation

| Key | Sent as |
|---|---|
| ↑ ↓ ← → | `up` / `down` / `left` / `right` |
| Home / End | `home` / `end` |
| Page Up / Down | `pageup` / `pagedown` |

### Function Keys

`F1` – `F12`

### Modifier Combinations

| Combination | Example | Support |
|---|---|---|
| Shift + key | Shift+Enter, Shift+F4 | ✅ |
| Ctrl + key | Ctrl+C, Ctrl+Z, Ctrl+A | ✅ |
| Alt + special key | Alt+F4 | ✅ |
| Alt + letter (Windows) | Alt+A | ✅ |
| Alt + letter (macOS) | Alt+A | ❌ OS converts to special characters |
| Win/Super key alone | Win | ❌ fyne limitation |
| IME keys (変換, 無変換) | — | ❌ fyne limitation |

---

## Localization

The UI language is automatically selected based on the OS locale.

| Locale | Language |
|---|---|
| `ja` | Japanese |
| `en` (others) | English |

Translation files are located in `gokeyshare/translations/`.
To add a new language, create `base.<language-code>.json` with the same keys.

---

## Security

- Without authentication or TLS, **anyone on the same network can connect and execute keystrokes** on the target machine.
- Always set `VKEYS_SECRET` and enable TLS when using over untrusted networks.
- Message size is capped at 1MB per event.
