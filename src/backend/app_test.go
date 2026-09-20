package backend

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"whatsweb/src/crypto"
)

func createTestApp(t *testing.T) (*App, func()) {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "whatsweb-backend-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	encryptionService, err := crypto.NewTestEncryptionService(tempDir)
	if err != nil {
		t.Fatalf("Failed to create encryption service: %v", err)
	}

	app := NewApp(encryptionService)

	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}

	return app, cleanup
}

func TestAppProfileLifecycle(t *testing.T) {
	app, cleanup := createTestApp(t)
	defer cleanup()

	// 1. Initially empty
	profiles := app.GetProfiles()
	if len(profiles) != 0 {
		t.Fatalf("Expected 0 profiles initially, got %d", len(profiles))
	}

	dtos := app.GetProfilesDTO()
	if len(dtos) != 0 {
		t.Fatalf("Expected 0 DTOs initially, got %d", len(dtos))
	}

	// 2. Create profile
	p1, err := app.CreateProfile("Personal")
	if err != nil {
		t.Fatalf("CreateProfile failed: %v", err)
	}
	if p1.ID == "" || p1.Name != "Personal" || p1.URL != "https://web.whatsapp.com" {
		t.Fatalf("Created profile has unexpected fields: %+v", p1)
	}

	// 3. Create second profile
	p2, err := app.CreateProfile("Work")
	if err != nil {
		t.Fatalf("CreateProfile failed: %v", err)
	}

	// 4. Verify profiles count and DTOs
	profiles = app.GetProfiles()
	if len(profiles) != 2 {
		t.Fatalf("Expected 2 profiles, got %d", len(profiles))
	}

	dtos = app.GetProfilesDTO()
	if len(dtos) != 2 {
		t.Fatalf("Expected 2 DTOs, got %d", len(dtos))
	}

	// 5. Active Profile
	app.SetActiveProfile(p1.ID)
	if app.GetActiveProfile() != p1.ID {
		t.Fatalf("Expected active profile %s, got %s", p1.ID, app.GetActiveProfile())
	}

	// 6. Update profile
	p1.Name = "Personal Renamed"
	if err := app.UpdateProfile(p1); err != nil {
		t.Fatalf("UpdateProfile failed: %v", err)
	}

	dtos = app.GetProfilesDTO()
	foundRenamed := false
	for _, dto := range dtos {
		if dto.ID == p1.ID && dto.Name == "Personal Renamed" {
			foundRenamed = true
		}
	}
	if !foundRenamed {
		t.Fatalf("Updated profile name not found in DTOs")
	}

	// 7. Delete profile
	if err := app.DeleteProfile(p2.ID); err != nil {
		t.Fatalf("DeleteProfile failed: %v", err)
	}

	profiles = app.GetProfiles()
	if len(profiles) != 1 {
		t.Fatalf("Expected 1 profile after deletion, got %d", len(profiles))
	}
}

func TestAppPersistence(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "whatsweb-persistence-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	encService1, err := crypto.NewTestEncryptionService(tempDir)
	if err != nil {
		t.Fatalf("Failed to create encryption service: %v", err)
	}

	app1 := NewApp(encService1)
	p, err := app1.CreateProfile("Persistent Profile")
	if err != nil {
		t.Fatalf("CreateProfile failed: %v", err)
	}

	// Simulate app restart
	encService2, err := crypto.NewTestEncryptionService(tempDir)
	if err != nil {
		t.Fatalf("Failed to create encryption service 2: %v", err)
	}

	app2 := NewApp(encService2)
	app2.Startup(context.Background())

	profiles := app2.GetProfiles()
	if len(profiles) != 1 {
		t.Fatalf("Expected 1 persisted profile, got %d", len(profiles))
	}
	if profiles[0].ID != p.ID || profiles[0].Name != "Persistent Profile" {
		t.Fatalf("Persisted profile mismatch: got %+v, want %+v", profiles[0], p)
	}
}

func TestAppWebviewManagement(t *testing.T) {
	app, cleanup := createTestApp(t)
	defer cleanup()

	p, err := app.CreateProfile("Test Webview Profile")
	if err != nil {
		t.Fatalf("CreateProfile failed: %v", err)
	}

	// Initially not running
	if app.IsProfileRunning(p.ID) {
		t.Fatalf("Expected profile not to be running initially")
	}

	// Test CloseProfile on non-running profile should not error
	if err := app.CloseProfile(p.ID); err != nil {
		t.Fatalf("CloseProfile on non-running profile failed: %v", err)
	}

	status := app.GetProfileStatus(p.ID)
	if status.ID != p.ID || status.IsRunning || status.HasWindow {
		t.Fatalf("Unexpected initial profile status: %+v", status)
	}

	allStatuses := app.GetAllProfileStatuses()
	if len(allStatuses) != 1 || allStatuses[0].ID != p.ID {
		t.Fatalf("Unexpected allStatuses: %+v", allStatuses)
	}
}

func TestEnsureSessionPreferences(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "whatsweb-prefs-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	if err := ensureSessionPreferences(tempDir); err != nil {
		t.Fatalf("ensureSessionPreferences failed: %v", err)
	}

	// Verify Preferences file
	prefsPath := filepath.Join(tempDir, "Default", "Preferences")
	data, err := os.ReadFile(prefsPath)
	if err != nil {
		t.Fatalf("Failed to read Preferences: %v", err)
	}

	var prefs map[string]any
	if err := json.Unmarshal(data, &prefs); err != nil {
		t.Fatalf("Failed to parse Preferences: %v", err)
	}

	bgMode, ok := prefs["background_mode"].(map[string]any)
	if !ok || bgMode["enabled"] != true {
		t.Fatalf("Expected background_mode.enabled == true, got %+v", bgMode)
	}

	// Verify Local State file
	localStatePath := filepath.Join(tempDir, "Local State")
	lsData, err := os.ReadFile(localStatePath)
	if err != nil {
		t.Fatalf("Failed to read Local State: %v", err)
	}

	var ls map[string]any
	if err := json.Unmarshal(lsData, &ls); err != nil {
		t.Fatalf("Failed to parse Local State: %v", err)
	}

	lsBgMode, ok := ls["background_mode"].(map[string]any)
	if !ok || lsBgMode["enabled"] != true {
		t.Fatalf("Expected Local State background_mode.enabled == true, got %+v", lsBgMode)
	}
}

