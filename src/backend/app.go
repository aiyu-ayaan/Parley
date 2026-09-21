package backend

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
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
	sessions          map[string]*session
	shuttingDown      bool
	procMu            sync.Mutex
	mu                sync.RWMutex
}

// NewApp creates a new App application struct
func NewApp(encryptionService *crypto.EncryptionService) *App {
	return &App{
		encryptionService: encryptionService,
		profiles:          make(map[string]*Profile),
		sessions:          make(map[string]*session),
	}
}

// Startup is called when the app starts
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.loadProfiles()
	for _, p := range a.GetProfiles() {
		a.startSession(p.ID, false)
	}
}

// DomReady is called after the frontend DOM is ready
func (a *App) DomReady(ctx context.Context) {
	// Emit profiles to frontend
	a.emitProfiles()
}

// Shutdown is called when the app is shutting down
func (a *App) Shutdown(ctx context.Context) {
	a.procMu.Lock()
	a.shuttingDown = true
	for _, s := range a.sessions {
		killGroup(s.cmd)
	}
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

	// Give the supervisor a moment to see the stop before wiping its data.
	time.Sleep(500 * time.Millisecond)
	_ = os.RemoveAll(a.sessionDir(id))

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

// OpenProfile shows the profile's WhatsApp window, starting its session if needed.
func (a *App) OpenProfile(id string) error {
	a.mu.RLock()
	_, ok := a.profiles[id]
	a.mu.RUnlock()
	if !ok {
		return fmt.Errorf("profile %s not found", id)
	}
	a.SetActiveProfile(id)
	a.procMu.Lock()
	s := a.sessions[id]
	open := s != nil && s.window
	a.procMu.Unlock()
	if open {
		focusWindow(id)
		return nil
	}
	a.startSession(id, true)
	return nil
}

// HideProfileWindow closes the window; the session keeps running headless.
func (a *App) HideProfileWindow(id string) error {
	a.procMu.Lock()
	defer a.procMu.Unlock()
	if s := a.sessions[id]; s != nil && s.window {
		killGroup(s.cmd)
	}
	return nil
}

// StartProfile starts a stopped profile in the background.
func (a *App) StartProfile(id string) error {
	a.startSession(id, false)
	return nil
}

// CloseProfile stops the profile's session entirely (no background notifications).
func (a *App) CloseProfile(id string) error {
	a.procMu.Lock()
	if s := a.sessions[id]; s != nil {
		s.stopped = true
		killGroup(s.cmd)
	}
	a.procMu.Unlock()
	return nil
}

// IsProfileRunning reports whether the profile has a live Chrome process.
func (a *App) IsProfileRunning(id string) bool {
	return a.GetProfileStatus(id).IsRunning
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
	st := ProfileStatus{ID: id}
	if p, ok := a.profiles[id]; ok {
		st.Name = p.Name
	}
	a.mu.RUnlock()

	a.procMu.Lock()
	if s := a.sessions[id]; s != nil && s.cmd != nil {
		st.IsRunning, st.HasWindow = true, s.window
	}
	a.procMu.Unlock()
	return st
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

func (a *App) emitStatus(id string) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "profile:status-changed", a.GetProfileStatus(id))
	}
}

// ShowWindow brings the dashboard back (used when Whatsweb is launched again).
func (a *App) ShowWindow() {
	if a.ctx != nil {
		runtime.WindowShow(a.ctx)
		runtime.WindowUnminimise(a.ctx)
	}
}

// Quit stops every session and exits Whatsweb.
func (a *App) Quit() {
	if a.ctx != nil {
		runtime.Quit(a.ctx)
	}
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
Exec=%s --hidden
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
