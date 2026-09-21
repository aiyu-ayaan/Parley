# Changelog

## [0.0.1-alpha.1](https://github.com/aiyu-ayaan/Parley/releases/tag/v0.0.1-alpha.1) (2026-09-21)

### Features

- Add install script with icon and app menu entry
- Keep accounts loaded in a hidden window so open is instant
- Redesign dashboard with discord style account rail
- Auto launch sessions in background and automatically persist on close
- Keep whatsapp session running in background on window close
- Launch isolated native webview session and add profile dashboard
- Add encryption service and profile management

### Bug fixes

- Show the app logo on the window and dock
- Play the system notification sound instead of whatsapp's tone
- Only release when a release PR lands, and use the new logo
- App window stuck on blank page and background tab refused on reused profiles
- Start accounts in a hidden tab so launch shows no window
- Closing a whatsapp window hides it instead of restarting chrome
- Open accounts as a bare app window instead of a tabbed chrome
- Keep account rail selection pill from being clipped
- Start sessions on dom ready so a second launch never touches them
- Stop leftover chrome holding a session dir before launch
- Run closed sessions as headless chrome and forward notifications
- Guard rapid restarts and force clean singleton locks
- Auto-restart chrome in background when user closes window
- Ensure immediate event binding and native window.go.main.App support
- Support window.go.backend.App binding, enable wails dev, and add dev/build scripts
- Resolve mutex deadlock, masterKey salt check, and random profile ID generation
- Remove unused imports

### Other changes

- First alpha with system notification sound and new logo
- Add marker driven release pr pipeline with channels
- Lighten background sessions and back off on failures
- Rename app to Parley and migrate whatsweb data
- Document background sessions, notifications and new ui
- Update README with correct dev workflow (wails build, not wails dev)
- Ignore build-support folder
- Update README with build instructions and workaround
- Ignore generated wailsjs folder
- Add wails.json config and update dependencies
- Add AGENTS.md with project instructions

