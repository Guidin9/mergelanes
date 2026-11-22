//go:build !windows

package assistant

// Outside Windows there is no per-user secret store we can count on without extra dependencies, so
// the key is a plain file in the user's config directory, readable only by them (0600).
const keyFileName = "gemini-key"

func protect(plain []byte) ([]byte, error) { return plain, nil }
func unprotect(enc []byte) ([]byte, error) { return enc, nil }
