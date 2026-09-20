package backend

import (
	"context"
	"crypto/rand"
	"log"
	"sync"

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
	mu                sync.RWMutex
}

// NewApp creates a new App application struct
func NewApp(encryptionService *crypto.EncryptionService) *App {
	return &App{
		encryptionService: encryptionService,
		profiles:          make(map[string]*Profile),
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

// OpenProfile opens a profile in the webview
func (a *App) OpenProfile(id string) {
	a.SetActiveProfile(id)
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "profile:open", id)
	}
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
