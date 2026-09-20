# Whatsweb

A lightweight Linux desktop application for WhatsApp Web with multi-account support, built with Go and Wails v2.

## Features

- **Lightweight** - Native desktop app with minimal resource usage
- **Multi-account support** - Run multiple WhatsApp profiles simultaneously (Discord-style circular UI)
- **Persistent sessions** - Profiles are saved encrypted, no need to re-login
- **Native notifications** - Get WhatsApp notifications like a native app
- **Auto-start option** - Launch on system startup
- **Encrypted storage** - All user data stored securely with AES-256 encryption

## Prerequisites

- Go 1.21 or later
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
git clone <repository-url>
cd Whatsweb

# Install dependencies
go mod tidy

# Create pkg-config workaround for webkit2gtk-4.1
mkdir -p pkgconfig
ln -sf /usr/lib/x86_64-linux-gnu/pkgconfig/webkit2gtk-4.1.pc pkgconfig/webkit2gtk-4.0.pc

# Build with Wails (production binary with packaging) - RECOMMENDED
PKG_CONFIG_PATH=./pkgconfig:$PKG_CONFIG_PATH ~/go/bin/wails build

# Production binary will be in build/bin/Whatsweb
```

## Running

### Development Workflow
```bash
# 1. Make changes to frontend (frontend/dist/) or backend (src/)

# 2. Rebuild and test (fast rebuild)
PKG_CONFIG_PATH=./pkgconfig:$PKG_CONFIG_PATH ~/go/bin/wails build

# 3. Run the binary
./build/bin/Whatsweb
```

> **Note**: 
> - `wails dev` requires a Vite dev server for hot reloading. This project uses static HTML/CSS/JS files, so `wails dev` will not work.
> - `go run .` directly won't work because Wails injects build tags at build time.
> - Use `wails build` + run binary for development iteration.

### Production
```bash
# After Wails build
./build/bin/Whatsweb
```

Or install system-wide:
```bash
sudo cp build/bin/Whatsweb /usr/local/bin/
whatsweb
```

## Usage

1. **First Launch**: Click the `+` button in the sidebar or the "Add Profile" button on the welcome screen
2. **Create Profile**: Enter a name (e.g., "Personal", "Work") and click "Create Profile"
3. **Scan QR Code**: WhatsApp Web will open - scan the QR code with your phone
4. **Add More**: Click `+` to add additional profiles
5. **Switch Accounts**: Click on profile circles in the sidebar to switch between accounts
6. **Right-click** a profile for rename/delete options

## Configuration

Profiles and encryption keys are stored in `~/.whatsweb/`:
- `salt` - Encryption salt (machine-specific)
- `profile-*.enc` - Encrypted profile data

## Security

- All profile data encrypted with AES-256-GCM
- Keys derived using PBKDF2 with 100,000 iterations
- Machine-specific key derivation (tied to `/etc/machine-id`)
- File permissions set to 0600 (owner read/write only)

## Project Structure

```
Whatsweb/
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
    │   └── app.go          # Backend logic (profile management)
    └── crypto/
        └── encryption.go   # Encryption service
```

## Development

### Adding Features

1. Backend: Add methods to `src/backend/app.go`
2. Frontend: Modify `frontend/dist/app.js` and `styles.css`
3. Test: `PKG_CONFIG_PATH=./pkgconfig:$PKG_CONFIG_PATH ~/go/bin/wails build && ./build/bin/Whatsweb`

### Bindings

Go methods are automatically exposed to JavaScript via Wails. Call them like:
```javascript
const result = await window.go.main.App.MethodName(args);
```

## Auto-start on Login

### systemd (user service)
```ini
# ~/.config/systemd/user/whatsweb.service
[Unit]
Description=Whatsweb
After=graphical-session.target

[Service]
ExecStart=/usr/local/bin/Whatsweb
Restart=on-failure

[Install]
WantedBy=default.target
```

```bash
systemctl --user enable --now whatsweb
```

## Troubleshooting

### WebView not loading / pkg-config errors
- Ensure webkit2gtk-4.1 is installed: `apt install libwebkit2gtk-4.1-dev`
- Create pkgconfig symlink (see Building section)
- Try: `wails doctor` to check environment

### Encryption errors
- Delete `~/.whatsweb/salt` to reset encryption (will lose saved profiles)

### Multiple instances
- Only one instance per user should run (profiles locked by encryption)

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
