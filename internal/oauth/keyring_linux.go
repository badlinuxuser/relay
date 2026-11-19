//go:build linux

package oauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"os/exec"
	"strings"
	"time"
)

// Secret Service is used when secret-tool is installed and an unlocked desktop keyring is
// available. Failure is non-fatal: the vault falls back to a private local master.key file.
func loadOSKeyringKey(account string) ([]byte, bool) {
	if _, err := exec.LookPath("secret-tool"); err != nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "secret-tool", "lookup", "service", "relay-oauth-vault", "account", account).Output()
	if err != nil {
		return nil, false
	}
	key, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(string(out)))
	if err != nil || len(key) != 32 {
		return nil, false
	}
	return key, true
}

func storeOSKeyringKey(account string, key []byte) bool {
	bin, err := exec.LookPath("secret-tool")
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "store", "--label=Relay OAuth vault", "service", "relay-oauth-vault", "account", account)
	cmd.Stdin = bytes.NewBufferString(base64.RawStdEncoding.EncodeToString(key))
	return cmd.Run() == nil
}
