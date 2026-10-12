package model

import "time"

// PackageRegistration reserves one logical package for a session-scoped key.
type PackageRegistration struct {
	SessionID      string
	IdempotencyKey string
	RequestHash    []byte
	PackageID      string
	CreatedAt      time.Time
}
