package backend

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"whatsweb/src/crypto"
)

// Profile represents a WhatsApp Web profile
type Profile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	LastUsed int64  `json:"last_used"`
}

// App struct
type App struct {
	ctx               context.Context
	encryptionService *crypto.EncryptionService
	profiles          map[string]*Profile
	activeProfile     string
	runningProcesses  map[string]*exec.Cmd
	stoppedSessions   map[string]bool // sessions intentionally stopped (won't auto-restart)
	shuttingDown      bool            // true when app is shutting down (no auto-restart)
	procMu            sync.Mutex
	mu                sync.RWMutex
}

// NewApp creates a new App application struct
func NewApp(encryptionService *crypto.EncryptionService) *App {
	return &App{
		encryptionService: encryptionService,
		profiles:          make(map[string]*Profile),
		runningProcesses:  make(map[string]*exec.Cmd),
		stoppedSessions:   make(map[string]bool),
	}
}

// Startup is called when the app starts
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.loadProfiles()
	go a.launchAllProfilesInBackground()
}

// DomReady is called after the frontend DOM is ready
func (a *App) DomReady(ctx context.Context) {
	// Emit profiles to frontend
	a.emitProfiles()
}

// BeforeClose is called before the app closes
func (a *App) BeforeClose(ctx context.Context) bool {
	return false // Allow close
}

// Shutdown is called when the app is shutting down
func (a *App) Shutdown(ctx context.Context) {
	a.procMu.Lock()
	a.shuttingDown = true
	a.procMu.Unlock()
	a.saveProfiles()
}

// loadProfiles loads profiles from encrypted storage
func (a *App) loadProfiles() {
	files, err := a.encryptionService.ListEncryptedFiles()
	if err != nil {
		log.Printf("Failed to list encrypted files: %v", err)
		return
	}

	for _, file := range files {
		encrypted, err := a.encryptionService.LoadEncrypted(file)
		if err != nil {
			log.Printf("Failed to load profile %s: %v", file, err)
			continue
		}

		var profile Profile
		if err := a.encryptionService.DecryptJSON(encrypted, &profile); err != nil {
			log.Printf("Failed to decrypt profile %s: %v", file, err)
			continue
		}

		a.mu.Lock()
		a.profiles[profile.ID] = &profile
		a.mu.Unlock()
	}
}

// saveProfiles saves all profiles to encrypted storage
func (a *App) saveProfiles() {
	a.mu.RLock()
	profilesCopy := make([]*Profile, 0, len(a.profiles))
	for _, profile := range a.profiles {
		profilesCopy = append(profilesCopy, profile)
	}
	a.mu.RUnlock()

	for _, profile := range profilesCopy {
		encrypted, err := a.encryptionService.EncryptJSON(profile)
		if err != nil {
			log.Printf("Failed to encrypt profile %s: %v", profile.ID, err)
			continue
		}

		filename := profile.ID + ".enc"
		if err := a.encryptionService.SaveEncrypted(filename, encrypted); err != nil {
			log.Printf("Failed to save profile %s: %v", profile.ID, err)
		}
	}
}

// emitProfiles emits the current profiles to the frontend
func (a *App) emitProfiles() {
	a.mu.RLock()
	profiles := make([]*Profile, 0, len(a.profiles))
	for _, p := range a.profiles {
		profiles = append(profiles, p)
	}
	a.mu.RUnlock()

	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "profiles:updated", profiles)
	}
}

// GetProfiles returns all profiles
func (a *App) GetProfiles() []*Profile {
	a.mu.RLock()
	defer a.mu.RUnlock()

	profiles := make([]*Profile, 0, len(a.profiles))
	for _, p := range a.profiles {
		profiles = append(profiles, p)
	}
	return profiles
}

// CreateProfile creates a new profile
func (a *App) CreateProfile(name string) (*Profile, error) {
	profile := &Profile{
		ID:   generateID(),
		Name: name,
		URL:  "https://web.whatsapp.com",
	}

	// Save immediately
	encrypted, err := a.encryptionService.EncryptJSON(profile)
	if err != nil {
		return nil, err
	}
	filename := profile.ID + ".enc"
	if err := a.encryptionService.SaveEncrypted(filename, encrypted); err != nil {
		return nil, err
	}

	a.mu.Lock()
	a.profiles[profile.ID] = profile
	a.mu.Unlock()

	a.emitProfiles()
	return profile, nil
}

// DeleteProfile deletes a profile
func (a *App) DeleteProfile(id string) error {
	_ = a.CloseProfile(id)

	a.mu.Lock()
	if _, exists := a.profiles[id]; !exists {
		a.mu.Unlock()
		return nil // Already deleted
	}
	delete(a.profiles, id)
	a.mu.Unlock()

	filename := id + ".enc"
	if err := a.encryptionService.DeleteEncrypted(filename); err != nil {
		return err
	}

	// Clean up session directory
	sessionDir := filepath.Join(a.encryptionService.DataDir(), "sessions", id)
	_ = os.RemoveAll(sessionDir)

	a.emitProfiles()
	return nil
}

// UpdateProfile updates a profile
func (a *App) UpdateProfile(profile *Profile) error {
	a.mu.Lock()
	if _, exists := a.profiles[profile.ID]; !exists {
		a.mu.Unlock()
		return nil
	}
	a.profiles[profile.ID] = profile
	a.mu.Unlock()

	encrypted, err := a.encryptionService.EncryptJSON(profile)
	if err != nil {
		return err
	}
	filename := profile.ID + ".enc"
	if err := a.encryptionService.SaveEncrypted(filename, encrypted); err != nil {
		return err
	}

	a.emitProfiles()
	return nil
}

// SetActiveProfile sets the active profile
func (a *App) SetActiveProfile(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.activeProfile = id
}

// GetActiveProfile returns the active profile
func (a *App) GetActiveProfile() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.activeProfile
}

// OpenProfile opens a profile in a dedicated webview session
func (a *App) OpenProfile(id string) error {
	a.SetActiveProfile(id)
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "profile:open", id)
	}
	return a.launchProfileWebview(id)
}

// HideProfileWindow hides/closes the profile window to the background
func (a *App) HideProfileWindow(id string) error {
	if _, err := exec.LookPath("xdotool"); err == nil {
		cmd := exec.Command("xdotool", "search", "--classname", fmt.Sprintf("whatsweb-%s", id), "windowclose")
		_ = cmd.Run()
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "profile:status-changed", a.GetProfileStatus(id))
		}
		return nil
	}
	if _, err := exec.LookPath("wmctrl"); err == nil {
		cmd := exec.Command("wmctrl", "-c", fmt.Sprintf("whatsweb-%s", id))
		_ = cmd.Run()
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "profile:status-changed", a.GetProfileStatus(id))
		}
		return nil
	}
	return nil
}

// CloseProfile closes a profile's webview process completely
func (a *App) CloseProfile(id string) error {
	a.procMu.Lock()
	a.stoppedSessions[id] = true // Mark as intentionally stopped — no auto-restart
	if cmd, running := a.runningProcesses[id]; running && cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		delete(a.runningProcesses, id)
	}
	a.procMu.Unlock()

	// Also terminate any background chrome processes for this session directory
	sessionDir := filepath.Join(a.encryptionService.DataDir(), "sessions", id)
	_ = exec.Command("pkill", "-TERM", "-f", fmt.Sprintf("user-data-dir=%s", sessionDir)).Run()

	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "profile:status-changed", a.GetProfileStatus(id))
	}
	return nil
}

// getSessionPID returns the PID of the profile process if running
func (a *App) getSessionPID(id string) int {
	a.procMu.Lock()
	if cmd, running := a.runningProcesses[id]; running && cmd != nil && cmd.Process != nil {
		if err := cmd.Process.Signal(syscall.Signal(0)); err == nil {
			a.procMu.Unlock()
			return cmd.Process.Pid
		}
		delete(a.runningProcesses, id)
	}
	a.procMu.Unlock()

	// Check if running from outside or previous launch
	sessionDir := filepath.Join(a.encryptionService.DataDir(), "sessions", id)
	out, err := exec.Command("pgrep", "-f", fmt.Sprintf("user-data-dir=%s", sessionDir)).Output()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		for _, line := range lines {
			if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && pid > 0 {
				return pid
			}
		}
	}
	return 0
}

// IsProfileRunning checks if a profile webview or background process is running
func (a *App) IsProfileRunning(id string) bool {
	return a.getSessionPID(id) > 0
}

// IsProfileWindowOpen checks if the profile's desktop window is currently visible
func (a *App) IsProfileWindowOpen(id string) bool {
	return isProfileWindowVisible(id)
}

// ProfileStatus represents the live state of a profile
type ProfileStatus struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsRunning bool   `json:"isRunning"`
	HasWindow bool   `json:"hasWindow"`
}

// GetProfileStatus returns the live status of a single profile
func (a *App) GetProfileStatus(id string) ProfileStatus {
	a.mu.RLock()
	name := ""
	if p, ok := a.profiles[id]; ok {
		name = p.Name
	}
	a.mu.RUnlock()

	running := a.IsProfileRunning(id)
	hasWindow := false
	if running {
		hasWindow = a.IsProfileWindowOpen(id)
	}

	return ProfileStatus{
		ID:        id,
		Name:      name,
		IsRunning: running,
		HasWindow: hasWindow,
	}
}

// GetAllProfileStatuses returns live status for all profiles
func (a *App) GetAllProfileStatuses() []ProfileStatus {
	a.mu.RLock()
	ids := make([]string, 0, len(a.profiles))
	for id := range a.profiles {
		ids = append(ids, id)
	}
	a.mu.RUnlock()

	statuses := make([]ProfileStatus, 0, len(ids))
	for _, id := range ids {
		statuses = append(statuses, a.GetProfileStatus(id))
	}
	return statuses
}

// launchAllProfilesInBackground launches all saved profiles in the background on app startup
func (a *App) launchAllProfilesInBackground() {
	time.Sleep(500 * time.Millisecond)

	a.mu.RLock()
	profiles := make([]*Profile, 0, len(a.profiles))
	for _, p := range a.profiles {
		profiles = append(profiles, p)
	}
	a.mu.RUnlock()

	for _, p := range profiles {
		if a.IsProfileRunning(p.ID) {
			continue
		}
		a.startSessionWithAutoRestart(p.ID, true)
	}

	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "profiles:status-synced", nil)
	}
}

// startSessionWithAutoRestart launches a Chrome session and automatically relaunches it
// in the background if the user closes the window. This keeps the WhatsApp Web session
// alive so notifications continue to arrive.
// If hideOnLaunch is true, the window is immediately hidden after spawning.
func (a *App) startSessionWithAutoRestart(id string, hideOnLaunch bool) {
	a.mu.RLock()
	profile, exists := a.profiles[id]
	a.mu.RUnlock()
	if !exists {
		return
	}

	sessionDir := filepath.Join(a.encryptionService.DataDir(), "sessions", id)
	_ = os.MkdirAll(sessionDir, 0700)
	_ = ensureSessionPreferences(sessionDir)
	_, _ = ensureBackgroundKeeperExtension(sessionDir)

	// Clean up stale singleton locks from previous crashes
	cleanStaleSingletonLock(sessionDir)

	targetURL := profile.URL
	if targetURL == "" {
		targetURL = "https://web.whatsapp.com"
	}

	// Clear the stopped flag — this session is now active
	a.procMu.Lock()
	delete(a.stoppedSessions, id)
	a.procMu.Unlock()

	cmd, err := createWebviewCommand(id, profile.Name, targetURL, sessionDir)
	if err != nil || cmd == nil {
		log.Printf("Failed to create webview command for %s: %v", id, err)
		return
	}

	// Detach Chrome from parent process group so it survives if Whatsweb exits
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		log.Printf("Failed to start session for %s: %v", id, err)
		return
	}

	a.procMu.Lock()
	a.runningProcesses[id] = cmd
	a.procMu.Unlock()

	go func(profileID string, c *exec.Cmd) {
		startTime := time.Now()

		if hideOnLaunch {
			// Wait for window to spawn, then hide it
			time.Sleep(1500 * time.Millisecond)
			silentlyUnmapProfileWindow(profileID)
		}

		// Wait for Chrome to exit
		_ = c.Wait()

		elapsed := time.Since(startTime)

		a.procMu.Lock()
		delete(a.runningProcesses, profileID)
		stopped := a.stoppedSessions[profileID]
		isShuttingDown := a.shuttingDown
		a.procMu.Unlock()

		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "profile:status-changed", a.GetProfileStatus(profileID))
		}

		// Auto-restart: if session wasn't intentionally stopped and app isn't shutting down,
		// relaunch Chrome in background so WhatsApp session stays alive
		if !stopped && !isShuttingDown {
			// Guard against rapid restart loops: if Chrome exited within 3 seconds,
			// it likely hit a stale lock or startup issue. Wait longer and force-clean.
			if elapsed < 3*time.Second {
				log.Printf("Session %s exited too quickly (%.1fs), force-cleaning locks and waiting before retry...", profileID, elapsed.Seconds())
				forceCleanSingletonLock(sessionDir)
				time.Sleep(2 * time.Second)
			} else {
				log.Printf("Session %s exited (user closed window), auto-restarting in background...", profileID)
				cleanStaleSingletonLock(sessionDir)
				time.Sleep(500 * time.Millisecond)
			}

			// Check again if we should restart (user might have stopped during the wait)
			a.procMu.Lock()
			stopped = a.stoppedSessions[profileID]
			isShuttingDown = a.shuttingDown
			a.procMu.Unlock()

			if !stopped && !isShuttingDown {
				a.startSessionWithAutoRestart(profileID, true)
			}
		}
	}(id, cmd)

	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "profile:status-changed", a.GetProfileStatus(id))
	}
}

// cleanStaleSingletonLock removes Chrome's SingletonLock/Socket/Cookie files if the
// process that owned them is no longer running. Stale locks prevent Chrome from launching.
func cleanStaleSingletonLock(sessionDir string) {
	lockPath := filepath.Join(sessionDir, "SingletonLock")
	target, err := os.Readlink(lockPath)
	if err != nil {
		return // No lock file
	}

	// Lock format: "hostname-pid"
	parts := strings.Split(target, "-")
	if len(parts) >= 2 {
		pidStr := parts[len(parts)-1]
		if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
			// Check if the process is still alive
			if err := syscall.Kill(pid, 0); err != nil {
				// Process is dead — remove stale locks
				log.Printf("Removing stale Chrome lock (PID %d is dead) in %s", pid, sessionDir)
				forceCleanSingletonLock(sessionDir)
			}
		}
	}
}

// forceCleanSingletonLock unconditionally removes all Chrome singleton lock files.
// Used when Chrome exited abnormally or too quickly and left stale locks behind.
func forceCleanSingletonLock(sessionDir string) {
	_ = os.Remove(filepath.Join(sessionDir, "SingletonLock"))
	_ = os.Remove(filepath.Join(sessionDir, "SingletonSocket"))
	_ = os.Remove(filepath.Join(sessionDir, "SingletonCookie"))
}

// silentlyUnmapProfileWindow unmaps/hides the profile window from desktop so it runs in background
func silentlyUnmapProfileWindow(id string) {
	targetClass := fmt.Sprintf("whatsweb-%s", id)
	if _, err := exec.LookPath("xdotool"); err == nil {
		out, err := exec.Command("xdotool", "search", "--classname", targetClass).Output()
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				wid := strings.TrimSpace(line)
				if wid != "" {
					_ = exec.Command("xdotool", "windowunmap", wid).Run()
				}
			}
		}
	}
}

// launchProfileWebview spawns or restores a dedicated webview window for the profile
func (a *App) launchProfileWebview(id string) error {
	a.mu.RLock()
	profile, exists := a.profiles[id]
	a.mu.RUnlock()
	if !exists {
		return fmt.Errorf("profile %s not found", id)
	}

	sessionDir := filepath.Join(a.encryptionService.DataDir(), "sessions", id)
	if err := os.MkdirAll(sessionDir, 0700); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}

	// Configure background mode and desktop notification permissions in preferences
	if err := ensureSessionPreferences(sessionDir); err != nil {
		log.Printf("Warning: failed to configure session preferences: %v", err)
	}
	if _, err := ensureBackgroundKeeperExtension(sessionDir); err != nil {
		log.Printf("Warning: failed to ensure background keeper extension: %v", err)
	}

	// 1. If window is already open and visible, bring it to front
	if isProfileWindowVisible(id) {
		log.Printf("Profile %s window is already visible, focusing it", id)
		focusProfileWindow(id)
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "profile:status-changed", a.GetProfileStatus(id))
		}
		return nil
	}

	targetURL := profile.URL
	if targetURL == "" {
		targetURL = "https://web.whatsapp.com"
	}

	// 2. If Chrome process is running in the background, send reopen command to active instance
	pid := a.getSessionPID(id)
	if pid > 0 {
		log.Printf("Profile %s background process is running (PID %d), triggering window reopen", id, pid)
		restoreCmd, err := createWebviewCommand(id, profile.Name, targetURL, sessionDir)
		if err == nil {
			restoreCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			_ = restoreCmd.Start()
			if a.ctx != nil {
				runtime.EventsEmit(a.ctx, "profile:status-changed", a.GetProfileStatus(id))
			}
			return nil
		}
	}

	// 3. Not running — launch fresh with auto-restart (visible window, not hidden)
	a.startSessionWithAutoRestart(id, false)
	return nil
}

// ensureBackgroundKeeperExtension generates a Chrome extension that keeps background service workers alive
func ensureBackgroundKeeperExtension(sessionDir string) (string, error) {
	extDir := filepath.Join(sessionDir, "keeper_ext")
	if err := os.MkdirAll(extDir, 0700); err != nil {
		return "", err
	}

	manifestPath := filepath.Join(extDir, "manifest.json")
	manifestContent := `{
  "name": "Whatsweb Background Keeper",
  "version": "1.0",
  "manifest_version": 3,
  "background": {
    "service_worker": "bg.js"
  },
  "permissions": [
    "background"
  ]
}`
	_ = os.WriteFile(manifestPath, []byte(manifestContent), 0600)

	bgPath := filepath.Join(extDir, "bg.js")
	bgContent := `console.log("Whatsweb background keeper service worker active");`
	_ = os.WriteFile(bgPath, []byte(bgContent), 0600)

	return extDir, nil
}

// ensureSessionPreferences ensures Chrome profile has background_mode and notifications enabled
func ensureSessionPreferences(sessionDir string) error {
	defaultDir := filepath.Join(sessionDir, "Default")
	if err := os.MkdirAll(defaultDir, 0700); err != nil {
		return err
	}

	// 1. Update Default/Preferences
	prefsPath := filepath.Join(defaultDir, "Preferences")
	var prefs map[string]any
	if data, err := os.ReadFile(prefsPath); err == nil {
		_ = json.Unmarshal(data, &prefs)
	}
	if prefs == nil {
		prefs = make(map[string]any)
	}

	bgMode, ok := prefs["background_mode"].(map[string]any)
	if !ok {
		bgMode = make(map[string]any)
		prefs["background_mode"] = bgMode
	}
	bgMode["enabled"] = true

	profile, ok := prefs["profile"].(map[string]any)
	if !ok {
		profile = make(map[string]any)
		prefs["profile"] = profile
	}
	contentSettings, ok := profile["content_settings"].(map[string]any)
	if !ok {
		contentSettings = make(map[string]any)
		profile["content_settings"] = contentSettings
	}
	exceptions, ok := contentSettings["exceptions"].(map[string]any)
	if !ok {
		exceptions = make(map[string]any)
		contentSettings["exceptions"] = exceptions
	}
	notifications, ok := exceptions["notifications"].(map[string]any)
	if !ok {
		notifications = make(map[string]any)
		exceptions["notifications"] = notifications
	}
	notifications["https://web.whatsapp.com:443,*"] = map[string]any{"setting": 1}

	sound, ok := exceptions["sound"].(map[string]any)
	if !ok {
		sound = make(map[string]any)
		exceptions["sound"] = sound
	}
	sound["https://web.whatsapp.com:443,*"] = map[string]any{"setting": 1}

	if out, err := json.MarshalIndent(prefs, "", "  "); err == nil {
		_ = os.WriteFile(prefsPath, out, 0600)
	}

	// 2. Update Local State
	localStatePath := filepath.Join(sessionDir, "Local State")
	var localState map[string]any
	if lsData, err := os.ReadFile(localStatePath); err == nil {
		_ = json.Unmarshal(lsData, &localState)
	}
	if localState == nil {
		localState = make(map[string]any)
	}
	lsBgMode, ok := localState["background_mode"].(map[string]any)
	if !ok {
		lsBgMode = make(map[string]any)
		localState["background_mode"] = lsBgMode
	}
	lsBgMode["enabled"] = true

	if out, err := json.MarshalIndent(localState, "", "  "); err == nil {
		_ = os.WriteFile(localStatePath, out, 0600)
	}

	return nil
}

// isProfileWindowVisible checks if a window with class whatsweb-<id> exists on the desktop
func isProfileWindowVisible(id string) bool {
	targetClass := fmt.Sprintf("whatsweb-%s", id)

	// Check with xdotool
	if _, err := exec.LookPath("xdotool"); err == nil {
		cmd := exec.Command("xdotool", "search", "--classname", targetClass)
		out, err := cmd.Output()
		if err == nil && len(strings.TrimSpace(string(out))) > 0 {
			return true
		}
	}

	// Check with wmctrl
	if _, err := exec.LookPath("wmctrl"); err == nil {
		cmd := exec.Command("wmctrl", "-x", "-l")
		out, err := cmd.Output()
		if err == nil && strings.Contains(string(out), targetClass) {
			return true
		}
	}

	return false
}

// focusProfileWindow activates and brings the profile window to the front
func focusProfileWindow(id string) bool {
	targetClass := fmt.Sprintf("whatsweb-%s", id)

	// If unmapped, map it back to desktop first
	if _, err := exec.LookPath("xdotool"); err == nil {
		out, err := exec.Command("xdotool", "search", "--classname", targetClass).Output()
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				wid := strings.TrimSpace(line)
				if wid != "" {
					_ = exec.Command("xdotool", "windowmap", wid).Run()
				}
			}
		}
	}

	if _, err := exec.LookPath("wmctrl"); err == nil {
		cmd := exec.Command("wmctrl", "-x", "-a", targetClass)
		if err := cmd.Run(); err == nil {
			return true
		}
	}

	if _, err := exec.LookPath("xdotool"); err == nil {
		cmd := exec.Command("xdotool", "search", "--classname", targetClass, "windowactivate")
		if err := cmd.Run(); err == nil {
			return true
		}
	}

	return false
}

// createWebviewCommand creates the command to run the webview with background mode enabled
func createWebviewCommand(id, name, targetURL, sessionDir string) (*exec.Cmd, error) {
	extDir, _ := ensureBackgroundKeeperExtension(sessionDir)

	browsers := []string{
		"google-chrome",
		"google-chrome-stable",
		"chromium",
		"chromium-browser",
		"brave-browser",
		"microsoft-edge",
	}

	for _, b := range browsers {
		if path, err := exec.LookPath(b); err == nil {
			args := []string{
				fmt.Sprintf("--app=%s", targetURL),
				fmt.Sprintf("--user-data-dir=%s", sessionDir),
				fmt.Sprintf("--class=whatsweb-%s", id),
				fmt.Sprintf("--name=whatsweb-%s", id),
				"--enable-background-mode",
				"--no-first-run",
				"--no-default-browser-check",
				"--password-store=basic",
			}
			if extDir != "" {
				args = append(args, fmt.Sprintf("--load-extension=%s", extDir))
			}
			return exec.Command(path, args...), nil
		}
	}

	// Fallback to Python3 WebKitGTK standalone window with hide-on-close
	if pyPath, err := exec.LookPath("python3"); err == nil {
		script := `
import sys, os, signal
import gi
try:
    gi.require_version('Gtk', '3.0')
    gi.require_version('WebKit2', '4.1')
except ValueError:
    gi.require_version('WebKit2', '4.0')
from gi.repository import Gtk, WebKit2, GLib

session_dir = sys.argv[1]
target_url = sys.argv[2]
title = sys.argv[3]

os.makedirs(session_dir, exist_ok=True)
data_mgr = WebKit2.WebsiteDataManager(
    base_data_directory=session_dir,
    base_cache_directory=os.path.join(session_dir, 'cache')
)
ctx = WebKit2.WebContext.new_with_website_data_manager(data_mgr)
view = WebKit2.WebView.new_with_context(ctx)
settings = view.get_settings()
settings.set_user_agent('Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36')
settings.set_enable_developer_extras(True)

win = Gtk.Window(title=title)
win.set_default_size(1024, 768)

# Close-to-background: hide window on close event so session and notifications stay active
def on_delete(window, event):
    window.hide()
    return True

win.connect('delete-event', on_delete)
win.add(view)
win.show_all()
view.load_uri(target_url)

# Handle SIGUSR1 to restore/unhide window
def on_sigusr1(signum, frame):
    GLib.idle_add(lambda: (win.show_all(), win.present()))

signal.signal(signal.SIGUSR1, on_sigusr1)

Gtk.main()
`
		return exec.Command(pyPath, "-c", script, sessionDir, targetURL, fmt.Sprintf("Whatsweb - %s", name)), nil
	}

	return nil, errors.New("no supported browser or WebKit engine found to launch webview")
}

// IsAutoStart returns true if autostart desktop entry exists
func (a *App) IsAutoStart() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	path := filepath.Join(home, ".config", "autostart", "whatsweb.desktop")
	_, err = os.Stat(path)
	return err == nil
}

// SetAutoStart enables or disables Linux desktop autostart
func (a *App) SetAutoStart(enabled bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	autostartDir := filepath.Join(home, ".config", "autostart")
	desktopPath := filepath.Join(autostartDir, "whatsweb.desktop")

	if !enabled {
		_ = os.Remove(desktopPath)
		return nil
	}

	if err := os.MkdirAll(autostartDir, 0755); err != nil {
		return err
	}

	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	content := fmt.Sprintf(`[Desktop Entry]
Type=Application
Version=1.0
Name=Whatsweb
Comment=WhatsApp Web Desktop Application
Exec=%s
Icon=whatsweb
Terminal=false
Categories=Network;InstantMessaging;
StartupNotify=true
`, exePath)

	return os.WriteFile(desktopPath, []byte(content), 0644)
}

// Minimize minimizes the application window
func (a *App) Minimize() {
	if a.ctx != nil {
		runtime.WindowMinimise(a.ctx)
	}
}

// generateID generates a simple unique ID
func generateID() string {
	return "profile-" + randomString(12)
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		for i := range b {
			b[i] = byte(i)
		}
	}
	result := make([]byte, n)
	for i := range b {
		result[i] = letters[int(b[i])%len(letters)]
	}
	return string(result)
}

// Minimal frontend binding types
type ProfileDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

func (a *App) GetProfilesDTO() []ProfileDTO {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := make([]ProfileDTO, 0, len(a.profiles))
	for _, p := range a.profiles {
		result = append(result, ProfileDTO{ID: p.ID, Name: p.Name, URL: p.URL})
	}
	return result
}

