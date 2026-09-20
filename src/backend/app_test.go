package backend

import (
	"context"
	"os"
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
}

