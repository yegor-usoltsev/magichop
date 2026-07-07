package protocol

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
)

var requestIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func NewRequestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func ValidRequestID(id string) bool {
	return requestIDPattern.MatchString(id)
}
