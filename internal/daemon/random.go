package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func randomID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}
