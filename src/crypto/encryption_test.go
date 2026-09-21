package crypto

import (
	"os"
	"path/filepath"
	"testing"
)

func createTestService(t *testing.T) (*EncryptionService, func()) {
	t.Helper()
	tempDir, err := os.MkdirTemp("", "parley-crypto-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	salt := []byte("0123456789abcdef")
	masterKey := []byte("01234567890123456789012345678901") // 32 bytes

	service := &EncryptionService{
		masterKey: masterKey,
		salt:      salt,
		dataDir:   tempDir,
	}

	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}

	return service, cleanup
}

func TestEncryptDecrypt(t *testing.T) {
	service, cleanup := createTestService(t)
	defer cleanup()

	originalText := "Hello, WhatsApp Web Encrypted Data!"
	encrypted, err := service.Encrypt([]byte(originalText))
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	if encrypted.Ciphertext == "" || encrypted.Nonce == "" || encrypted.Salt == "" {
		t.Fatalf("Encrypted payload has empty fields: %+v", encrypted)
	}

	decrypted, err := service.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if string(decrypted) != originalText {
		t.Fatalf("Decrypted text mismatch: got %q, want %q", string(decrypted), originalText)
	}
}

type testPayload struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

func TestEncryptDecryptJSON(t *testing.T) {
	service, cleanup := createTestService(t)
	defer cleanup()

	payload := testPayload{
		ID:   "profile-123",
		Name: "Personal Account",
		URL:  "https://web.whatsapp.com",
	}

	encrypted, err := service.EncryptJSON(payload)
	if err != nil {
		t.Fatalf("EncryptJSON failed: %v", err)
	}

	var decrypted testPayload
	if err := service.DecryptJSON(encrypted, &decrypted); err != nil {
		t.Fatalf("DecryptJSON failed: %v", err)
	}

	if decrypted != payload {
		t.Fatalf("JSON payload mismatch: got %+v, want %+v", decrypted, payload)
	}
}

func TestFileStorage(t *testing.T) {
	service, cleanup := createTestService(t)
	defer cleanup()

	payload := testPayload{ID: "profile-test", Name: "Work"}
	encrypted, err := service.EncryptJSON(payload)
	if err != nil {
		t.Fatalf("EncryptJSON failed: %v", err)
	}

	filename := "profile-test.enc"
	if err := service.SaveEncrypted(filename, encrypted); err != nil {
		t.Fatalf("SaveEncrypted failed: %v", err)
	}

	// Verify file exists on disk
	expectedPath := filepath.Join(service.dataDir, filename)
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Fatalf("Saved file does not exist at %s", expectedPath)
	}

	loaded, err := service.LoadEncrypted(filename)
	if err != nil {
		t.Fatalf("LoadEncrypted failed: %v", err)
	}

	var loadedPayload testPayload
	if err := service.DecryptJSON(loaded, &loadedPayload); err != nil {
		t.Fatalf("DecryptJSON of loaded data failed: %v", err)
	}

	if loadedPayload != payload {
		t.Fatalf("Loaded payload mismatch: got %+v, want %+v", loadedPayload, payload)
	}

	// Test ListEncryptedFiles
	files, err := service.ListEncryptedFiles()
	if err != nil {
		t.Fatalf("ListEncryptedFiles failed: %v", err)
	}
	if len(files) != 1 || files[0] != filename {
		t.Fatalf("ListEncryptedFiles unexpected result: %v", files)
	}

	// Test DeleteEncrypted
	if err := service.DeleteEncrypted(filename); err != nil {
		t.Fatalf("DeleteEncrypted failed: %v", err)
	}
	filesAfter, err := service.ListEncryptedFiles()
	if err != nil {
		t.Fatalf("ListEncryptedFiles after delete failed: %v", err)
	}
	if len(filesAfter) != 0 {
		t.Fatalf("Expected 0 files after delete, got %d", len(filesAfter))
	}
}
