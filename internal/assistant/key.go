package assistant

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// The API key belongs to the person using the computer, so it lives in their own profile (never in
// the shared state folder or the repository): on Windows encrypted with DPAPI for the current user,
// elsewhere in a file only they can read. GEMINI_API_KEY in the environment overrides it.

// ErrNoKey means no key has been saved and GEMINI_API_KEY is not set.
var ErrNoKey = errors.New("no Gemini API key set")

func keyPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "MergeLanes", keyFileName), nil
}

// LoadKey returns the key and where it came from ("env" or "saved").
func LoadKey() (key, source string, err error) {
	if k := strings.TrimSpace(os.Getenv("GEMINI_API_KEY")); k != "" {
		return k, "env", nil
	}
	p, err := keyPath()
	if err != nil {
		return "", "", err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", ErrNoKey
	}
	if err != nil {
		return "", "", err
	}
	plain, err := unprotect(b)
	if err != nil {
		return "", "", err
	}
	if k := strings.TrimSpace(string(plain)); k != "" {
		return k, "saved", nil
	}
	return "", "", ErrNoKey
}

// SaveKey stores the key for the current user; an empty key deletes it.
func SaveKey(key string) error {
	p, err := keyPath()
	if err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := protect([]byte(key))
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
