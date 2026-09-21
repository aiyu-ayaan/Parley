# Parley

A lightweight Linux desktop application for WhatsApp Web with multi-account support, built with Go and Wails v2.

## Features

- **Lightweight** - Native desktop app with minimal resource usage
- **Multi-account support** - Run multiple WhatsApp profiles simultaneously (Discord-style circular UI)
- **Persistent sessions** - Profiles are saved encrypted, no need to re-login
- **Always-on background** - Close the window and the account keeps running headless, still notifying
- **Native notifications** - Desktop notifications for every account; click one to open that account
- **Auto-start option** - Launch on system startup
- **Encrypted storage** - All user data stored securely with AES-256 encryption

## Prerequisites

- Go 1.21 or later
- A Chromium-based browser (`google-chrome`, `chromium`, `brave-browser` or `microsoft-edge`)
- `notify-send` (package `libnotify-bin` / `libnotify`)
- Wails v2 CLI (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)
- **WebView2 dependencies** (Linux: webkit2gtk-4.1)

### Linux Dependencies

```bash
# Ubuntu/Debian (22.04+ has webkit2gtk-4.1)
sudo apt update && sudo apt install -y libwebkit2gtk-4.1-dev libgtk-3-dev libayatana-appindicator3-dev pkg-config

# Fedora
sudo dnf install webkit2gtk4.1-devel gtk3-devel libayatana-appindicator-gtk3-devel

# Arch
sudo pacman -S webkit2gtk-4.1 gtk3 libayatana-appindicator
```

> **Note**: Wails expects `webkit2gtk-4.0` but Ubuntu 22.04+ ships `webkit2gtk-4.1`. See [Building](#building-from-source) for workaround.

## Building from Source

```bash
# Clone the repository
git clone https://github.com/aiyu-ayaan/Whatsweb.git
cd Whatsweb

# Install dependencies
go mod tidy

# Create pkg-config workaround for webkit2gtk-4.1
mkdir -p pkgconfig
ln -sf /usr/lib/x86_64-linux-gnu/pkgconfig/webkit2gtk-4.1.pc pkgconfig/webkit2gtk-4.0.pc

# Build with Wails (production binary with packaging) - RECOMMENDED
# Build with script (handles PKG_CONFIG_PATH automatically)
./build.sh

# Or build manually with Wails:
PKG_CONFIG_PATH=./pkgconfig:$PKG_CONFIG_PATH ~/go/bin/wails build

# Production binary will be in build/bin/parley
```

## Running

### Development Mode (with Live Reload)
```bash
# Run dev mode directly
./dev.sh

# Or run via wails dev manually:
PKG_CONFIG_PATH=./pkgconfig:$PKG_CONFIG_PATH ~/go/bin/wails dev
```

### Production Build & Run
```bash
# Rebuild binary
./build.sh

# Run binary
./build/bin/parley
```

### Production
```bash
# After Wails build
./build/bin/parley
```

Or install system-wide:
```bash
sudo cp build/bin/parley /usr/local/bin/
parley
```

## Usage

1. **Add an account**: Click `+` in the rail, enter a name (e.g. "Work"). A WhatsApp window opens; scan the QR code.
2. **Close the WhatsApp window** when done. The account keeps running headless and keeps notifying.
3. **Accounts rail**: click an avatar to manage it, double-click to open its window. The dot shows state:
   green = window open, blue = background, grey = stopped.
4. **Rename** by editing the name in the account panel. **Remove** logs out and deletes its data.
5. **Settings** (top icon): launch at login, and Quit.

Closing the Parley dashboard only hides it. Launch Parley again to bring it back. Use **Settings → Quit** to stop everything.

## How background works

Each account is a Chrome process with its own data dir (`~/.parley/sessions/<id>`), supervised by Parley:

- **Window open**: `chrome --app=https://web.whatsapp.com`. Chrome shows notifications itself.
- **Window closed**: Chrome exits, and Parley relaunches it `--headless=new` on the same data dir, so the login is kept.
  Over a private DevTools pipe (`--remote-debugging-pipe`, no TCP port) it injects a hook that forwards
  every WhatsApp notification to `notify-send`. The page reports itself hidden, so chats are not marked as read.
- Clicking a notification switches that account back to window mode.

## Configuration

Profiles and encryption keys are stored in `~/.parley/`:
- `salt` - Encryption salt (machine-specific)
- `profile-*.enc` - Encrypted profile data

## Security

- All profile data encrypted with AES-256-GCM
- Keys derived using PBKDF2 with 100,000 iterations
- Machine-specific key derivation (tied to `/etc/machine-id`)
- File permissions set to 0600 (owner read/write only)

## Project Structure

```
Parley/
├── main.go                 # Application entry point
├── go.mod                  # Go module definition
├── README.md               # This file
├── wails.json              # Wails configuration
├── .gitignore
├── frontend/
│   └── dist/               # Frontend assets (HTML, CSS, JS)
│       ├── index.html      # Main HTML
│       ├── styles.css      # Styling (WhatsApp dark theme)
│       └── app.js          # Frontend logic
├── pkgconfig/              # pkg-config workaround (gitignored)
└── src/
    ├── backend/
    │   ├── app.go          # Profiles, bindings, autostart
    │   └── session.go      # Chrome supervisor, headless mode, notification forwarding
    └── crypto/
        └── encryption.go   # Encryption service
```

## Development

### Adding Features

1. Backend: Add methods to `src/backend/app.go`
2. Frontend: Modify `frontend/dist/app.js` and `styles.css`
3. Test: `PKG_CONFIG_PATH=./pkgconfig:$PKG_CONFIG_PATH ~/go/bin/wails build && ./build/bin/parley`

### Bindings

Go methods are automatically exposed to JavaScript via Wails. Call them like:
```javascript
const result = await window.go.backend.App.MethodName(args);
```

## Auto-start on Login

Easiest: Settings → **Launch at login** (writes `~/.config/autostart/parley.desktop` with `--hidden`).

### systemd (user service)
```ini
# ~/.config/systemd/user/parley.service
[Unit]
Description=Parley
After=graphical-session.target

[Service]
ExecStart=/usr/local/bin/parley --hidden
Restart=on-failure

[Install]
WantedBy=default.target
```

```bash
systemctl --user enable --now parley
```

## Troubleshooting

### WebView not loading / pkg-config errors
- Ensure webkit2gtk-4.1 is installed: `apt install libwebkit2gtk-4.1-dev`
- Create pkgconfig symlink (see Building section)
- Try: `wails doctor` to check environment

### Encryption errors
- Delete `~/.parley/salt` to reset encryption (will lose saved profiles)

### Multiple instances
- Parley is single-instance: launching it again shows the running dashboard.

### No notifications in background
- Check `notify-send test` works and a Chromium-based browser is installed.

### "Overriding existing handler for signal 10" warning
- This is a normal WebKit/JSC warning, not an error. The app works correctly.

### "wails dev" fails with Vite timeout
- This project uses static frontend files, not Vite. Use `wails build` instead of `wails dev`.

### "go run" fails with build tags error
- Wails injects required build tags at build time. Use `wails build` instead of `go run`.

## License

MIT License - Feel free to use and modify.

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Submit a PR

Commit format: `type(scope): message`
- Types: feat, fix, docs, refactor, test, chore
- Scopes: ui, backend, crypto, build
