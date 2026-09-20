package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/pbkdf2"
)

const (
	keyLength    = 32 // AES-256
	saltLength   = 16
	iterations   = 100000
)

// EncryptionService handles encryption/decryption of sensitive data
type EncryptionService struct {
	masterKey []byte
	salt      []byte
	dataDir   string
}

// EncryptedData represents encrypted data with metadata
type EncryptedData struct {
	Salt      string `json:"salt"`
	Ciphertext string `json:"ciphertext"`
	Nonce     string `json:"nonce"`
}

// NewEncryptionService creates a new encryption service with default directory
func NewEncryptionService() (*EncryptionService, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dataDir := filepath.Join(homeDir, ".whatsweb")
	return NewEncryptionServiceWithDir(dataDir)
}

// NewEncryptionServiceWithDir creates an encryption service with a specific directory
func NewEncryptionServiceWithDir(dataDir string) (*EncryptionService, error) {
	// Create directory if it doesn't exist
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}

	// Load or generate salt
	saltPath := filepath.Join(dataDir, "salt")
	var salt []byte
	if _, err := os.Stat(saltPath); os.IsNotExist(err) {
		// Generate new salt
		salt = make([]byte, saltLength)
		if _, err := rand.Read(salt); err != nil {
			return nil, err
		}
		if err := os.WriteFile(saltPath, salt, 0600); err != nil {
			return nil, err
		}
	} else {
		salt, err = os.ReadFile(saltPath)
		if err != nil {
			return nil, err
		}
	}

	// For master key, derive from machine ID and salt
	machineID := getMachineID()
	masterKey := pbkdf2.Key([]byte(machineID), salt, iterations, keyLength, sha256.New)

	return &EncryptionService{
		masterKey: masterKey,
		salt:      salt,
		dataDir:   dataDir,
	}, nil
}

// NewTestEncryptionService creates an encryption service for testing in a custom directory
func NewTestEncryptionService(dataDir string) (*EncryptionService, error) {
	return NewEncryptionServiceWithDir(dataDir)
}

// DataDir returns the base directory where data is stored
func (e *EncryptionService) DataDir() string {
	return e.dataDir
}

// getMachineID returns a machine-specific identifier
func getMachineID() string {
	// Try to read machine-id
	if data, err := os.ReadFile("/etc/machine-id"); err == nil {
		return string(data)
	}
	// Fallback to hostname
	if hostname, err := os.Hostname(); err == nil {
		return hostname
	}
	return "default-machine-id"
}

// Encrypt encrypts plaintext data
func (e *EncryptionService) Encrypt(plaintext []byte) (*EncryptedData, error) {
	block, err := aes.NewCipher(e.masterKey)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	return &EncryptedData{
		Salt:       base64.StdEncoding.EncodeToString(e.salt),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
	}, nil
}

// Decrypt decrypts encrypted data
func (e *EncryptionService) Decrypt(data *EncryptedData) ([]byte, error) {
	salt, err := base64.StdEncoding.DecodeString(data.Salt)
	if err != nil {
		return nil, err
	}

	ciphertext, err := base64.StdEncoding.DecodeString(data.Ciphertext)
	if err != nil {
		return nil, err
	}

	nonce, err := base64.StdEncoding.DecodeString(data.Nonce)
	if err != nil {
		return nil, err
	}

	// Use master key if salt matches, otherwise derive from salt
	var key []byte
	if len(e.masterKey) == keyLength && (len(e.salt) == 0 || string(salt) == string(e.salt)) {
		key = e.masterKey
	} else {
		key = pbkdf2.Key([]byte(getMachineID()), salt, iterations, keyLength, sha256.New)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.New("decryption failed: invalid key or corrupted data")
	}

	return plaintext, nil
}

// EncryptJSON encrypts a struct to JSON then encrypts it
func (e *EncryptionService) EncryptJSON(v interface{}) (*EncryptedData, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return e.Encrypt(data)
}

// DecryptJSON decrypts and unmarshals JSON
func (e *EncryptionService) DecryptJSON(data *EncryptedData, v interface{}) error {
	plaintext, err := e.Decrypt(data)
	if err != nil {
		return err
	}
	return json.Unmarshal(plaintext, v)
}

// SaveEncrypted saves encrypted data to file
func (e *EncryptionService) SaveEncrypted(filename string, data *EncryptedData) error {
	filepath := filepath.Join(e.dataDir, filename)
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath, jsonData, 0600)
}

// LoadEncrypted loads encrypted data from file
func (e *EncryptionService) LoadEncrypted(filename string) (*EncryptedData, error) {
	filepath := filepath.Join(e.dataDir, filename)
	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, err
	}
	var encrypted EncryptedData
	if err := json.Unmarshal(data, &encrypted); err != nil {
		return nil, err
	}
	return &encrypted, nil
}

// ListEncryptedFiles lists all encrypted profile files
func (e *EncryptionService) ListEncryptedFiles() ([]string, error) {
	entries, err := os.ReadDir(e.dataDir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".enc" {
			files = append(files, entry.Name())
		}
	}
	return files, nil
}

// DeleteEncrypted deletes an encrypted file
func (e *EncryptionService) DeleteEncrypted(filename string) error {
	filepath := filepath.Join(e.dataDir, filename)
	return os.Remove(filepath)
}
