package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"io"
	"time"
)

// Secret represents an encrypted secret.
type Secret struct {
	Namespace   string
	Name        string
	SecretType  string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	UsedByCount int // Computed: how many pollers reference this
}

// CreateSecret creates a new encrypted secret.
func (s *Store) CreateSecret(namespace, name, secretType, plaintext string) error {
	if s.secretKey == nil {
		return fmt.Errorf("secret key not configured")
	}

	encrypted, nonce, err := s.encrypt([]byte(plaintext))
	if err != nil {
		return fmt.Errorf("encrypt secret: %w", err)
	}

	now := time.Now()
	_, err = s.db.Exec(`
		INSERT INTO secrets (namespace, name, secret_type, encrypted_value, nonce, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, namespace, name, secretType, encrypted, nonce, now, now)

	return err
}

// UpdateSecret updates an existing secret.
func (s *Store) UpdateSecret(namespace, name, plaintext string) error {
	if s.secretKey == nil {
		return fmt.Errorf("secret key not configured")
	}

	encrypted, nonce, err := s.encrypt([]byte(plaintext))
	if err != nil {
		return fmt.Errorf("encrypt secret: %w", err)
	}

	result, err := s.db.Exec(`
		UPDATE secrets SET encrypted_value = ?, nonce = ?, updated_at = ?
		WHERE namespace = ? AND name = ?
	`, encrypted, nonce, time.Now(), namespace, name)
	if err != nil {
		return err
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("secret not found")
	}
	return nil
}

// GetSecretValue retrieves and decrypts a secret value.
func (s *Store) GetSecretValue(namespace, name string) (string, error) {
	if s.secretKey == nil {
		return "", fmt.Errorf("secret key not configured")
	}

	var encrypted, nonce []byte
	err := s.db.QueryRow(`
		SELECT encrypted_value, nonce FROM secrets WHERE namespace = ? AND name = ?
	`, namespace, name).Scan(&encrypted, &nonce)

	if err == sql.ErrNoRows {
		return "", fmt.Errorf("secret not found: %s", name)
	}
	if err != nil {
		return "", fmt.Errorf("query secret: %w", err)
	}

	plaintext, err := s.decrypt(encrypted, nonce)
	if err != nil {
		return "", fmt.Errorf("decrypt secret: %w", err)
	}

	return string(plaintext), nil
}

// GetSecret retrieves secret metadata (without value).
func (s *Store) GetSecret(namespace, name string) (*Secret, error) {
	var secret Secret
	err := s.db.QueryRow(`
		SELECT namespace, name, secret_type, created_at, updated_at
		FROM secrets WHERE namespace = ? AND name = ?
	`, namespace, name).Scan(&secret.Namespace, &secret.Name, &secret.SecretType,
		&secret.CreatedAt, &secret.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query secret: %w", err)
	}

	// Count usage
	secret.UsedByCount, _ = s.countSecretUsage(namespace, name)

	return &secret, nil
}

// ListSecrets returns all secrets in a namespace (without values).
func (s *Store) ListSecrets(namespace string) ([]*Secret, error) {
	rows, err := s.db.Query(`
		SELECT namespace, name, secret_type, created_at, updated_at
		FROM secrets WHERE namespace = ? ORDER BY name
	`, namespace)
	if err != nil {
		return nil, fmt.Errorf("query secrets: %w", err)
	}
	defer rows.Close()

	var secrets []*Secret
	for rows.Next() {
		var secret Secret
		if err := rows.Scan(&secret.Namespace, &secret.Name, &secret.SecretType,
			&secret.CreatedAt, &secret.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan secret: %w", err)
		}

		// Count usage
		secret.UsedByCount, _ = s.countSecretUsage(namespace, secret.Name)

		secrets = append(secrets, &secret)
	}

	return secrets, rows.Err()
}

// DeleteSecret deletes a secret.
func (s *Store) DeleteSecret(namespace, name string, force bool) (int, error) {
	// Check if in use
	usageCount, err := s.countSecretUsage(namespace, name)
	if err != nil {
		return 0, err
	}

	if usageCount > 0 && !force {
		return 0, fmt.Errorf("secret is used by %d pollers (use force to delete)", usageCount)
	}

	result, err := s.db.Exec(`
		DELETE FROM secrets WHERE namespace = ? AND name = ?
	`, namespace, name)
	if err != nil {
		return 0, err
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return 0, fmt.Errorf("secret not found")
	}

	return usageCount, nil
}

// SecretExists checks if a secret exists.
func (s *Store) SecretExists(namespace, name string) (bool, error) {
	var count int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM secrets WHERE namespace = ? AND name = ?
	`, namespace, name).Scan(&count)
	return count > 0, err
}

// countSecretUsage counts how many pollers reference a secret.
func (s *Store) countSecretUsage(namespace, secretName string) (int, error) {
	secretRef := "secret:" + secretName

	// Check in namespace defaults
	var nsCount int
	s.db.QueryRow(`
		SELECT COUNT(*) FROM namespaces 
		WHERE name = ? AND (
			config LIKE ? OR config LIKE ?
		)
	`, namespace, "%"+secretRef+"%", "%"+secretRef+"%").Scan(&nsCount)

	// Check in target defaults
	var targetCount int
	s.db.QueryRow(`
		SELECT COUNT(*) FROM targets 
		WHERE namespace = ? AND (
			config LIKE ? OR config LIKE ?
		)
	`, namespace, "%"+secretRef+"%", "%"+secretRef+"%").Scan(&targetCount)

	// Check in poller configs
	var pollerCount int
	s.db.QueryRow(`
		SELECT COUNT(*) FROM pollers 
		WHERE namespace = ? AND (
			protocol_config LIKE ? OR protocol_config LIKE ?
		)
	`, namespace, "%"+secretRef+"%", "%"+secretRef+"%").Scan(&pollerCount)

	return nsCount + targetCount + pollerCount, nil
}

// encrypt encrypts data using AES-256-GCM.
func (s *Store) encrypt(plaintext []byte) (ciphertext, nonce []byte, err error) {
	block, err := aes.NewCipher(s.secretKey)
	if err != nil {
		return nil, nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}

	nonce = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}

	ciphertext = gcm.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

// decrypt decrypts data using AES-256-GCM.
func (s *Store) decrypt(ciphertext, nonce []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.secretKey)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}

// EncryptPassword encrypts a password for storage directly in poller config.
// Returns a base64-encoded string that can be stored in JSON.
func (s *Store) EncryptPassword(plaintext string) (string, error) {
	if s.secretKey == nil {
		return "", fmt.Errorf("secret key not configured")
	}

	encrypted, nonce, err := s.encrypt([]byte(plaintext))
	if err != nil {
		return "", err
	}

	// Combine nonce + ciphertext and encode
	combined := append(nonce, encrypted...)
	return encodeBase64(combined), nil
}

// DecryptPassword decrypts a password from poller config.
func (s *Store) DecryptPassword(encoded string) (string, error) {
	if s.secretKey == nil {
		return "", fmt.Errorf("secret key not configured")
	}

	combined, err := decodeBase64(encoded)
	if err != nil {
		return "", fmt.Errorf("decode password: %w", err)
	}

	block, err := aes.NewCipher(s.secretKey)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(combined) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce := combined[:nonceSize]
	ciphertext := combined[nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// HasSecretKey returns true if a secret key is configured.
func (s *Store) HasSecretKey() bool {
	return s.secretKey != nil
}

// Base64 encoding helpers
func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func decodeBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
