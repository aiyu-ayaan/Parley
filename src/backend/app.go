package backend

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"

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
	procMu            sync.Mutex
	mu                sync.RWMutex
}

// NewApp creates a new App application struct
func NewApp(encryptionService *crypto.EncryptionService) *App {
	return &App{
		encryptionService: encryptionService,
		profiles:          make(map[string]*Profile),
		runningProcesses:  make(map[string]*exec.Cmd),
	}
}

// Startup is called when the app starts
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.loadProfiles()
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

// CloseProfile closes a profile's webview process
func (a *App) CloseProfile(id string) error {
	a.procMu.Lock()
	defer a.procMu.Unlock()

	if cmd, running := a.runningProcesses[id]; running && cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		delete(a.runningProcesses, id)
	}
	return nil
}

// IsProfileRunning checks if a profile webview is running
func (a *App) IsProfileRunning(id string) bool {
	a.procMu.Lock()
	defer a.procMu.Unlock()

	if cmd, running := a.runningProcesses[id]; running && cmd != nil && cmd.Process != nil {
		if err := cmd.Process.Signal(syscall.Signal(0)); err == nil {
			return true
		}
		delete(a.runningProcesses, id)
	}
	return false
}

// launchProfileWebview spawns a dedicated webview window for the profile
func (a *App) launchProfileWebview(id string) error {
	a.mu.RLock()
	profile, exists := a.profiles[id]
	a.mu.RUnlock()
	if !exists {
		return fmt.Errorf("profile %s not found", id)
	}

	a.procMu.Lock()
	if cmd, running := a.runningProcesses[id]; running && cmd != nil && cmd.Process != nil {
		if err := cmd.Process.Signal(syscall.Signal(0)); err == nil {
			a.procMu.Unlock()
			log.Printf("Profile %s is already running", id)
			return nil
		}
	}
	a.procMu.Unlock()

	sessionDir := filepath.Join(a.encryptionService.DataDir(), "sessions", id)
	if err := os.MkdirAll(sessionDir, 0700); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}

	targetURL := profile.URL
	if targetURL == "" {
		targetURL = "https://web.whatsapp.com"
	}

	cmd, err := createWebviewCommand(id, profile.Name, targetURL, sessionDir)
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start webview: %w", err)
	}

	a.procMu.Lock()
	a.runningProcesses[id] = cmd
	a.procMu.Unlock()

	go func() {
		_ = cmd.Wait()
		a.procMu.Lock()
		delete(a.runningProcesses, id)
		a.procMu.Unlock()
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "profile:closed", id)
		}
	}()

	return nil
}

// createWebviewCommand creates the command to run the webview
func createWebviewCommand(id, name, targetURL, sessionDir string) (*exec.Cmd, error) {
	// Look for Chromium-based browser for full WhatsApp Web desktop app mode
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
				"--no-first-run",
				"--no-default-browser-check",
			}
			return exec.Command(path, args...), nil
		}
	}

	// Fallback to Python3 WebKitGTK standalone window
	if pyPath, err := exec.LookPath("python3"); err == nil {
		script := `
import sys, os
import gi
try:
    gi.require_version('Gtk', '3.0')
    gi.require_version('WebKit2', '4.1')
except ValueError:
    gi.require_version('WebKit2', '4.0')
from gi.repository import Gtk, WebKit2

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
win.connect('destroy', Gtk.main_quit)
win.add(view)
win.show_all()
view.load_uri(target_url)
Gtk.main()
`
		return exec.Command(pyPath, "-c", script, sessionDir, targetURL, fmt.Sprintf("Whatsweb - %s", name)), nil
	}

	return nil, errors.New("no supported browser or WebKit engine found to launch webview")
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
