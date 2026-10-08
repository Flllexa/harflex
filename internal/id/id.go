package id

import (
	"crypto/rand"
	"encoding/hex"
)

func New() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic("generate id: " + err.Error())
	}

	return hex.EncodeToString(bytes[:])
}
