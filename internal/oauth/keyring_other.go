//go:build !linux

package oauth

// Platforms without the built-in Secret Service adapter use the encrypted local-key fallback.
func loadOSKeyringKey(string) ([]byte, bool) { return nil, false }
func storeOSKeyringKey(string, []byte) bool  { return false }
