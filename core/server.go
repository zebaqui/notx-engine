package core

import "time"

// Server represents a trusted peer notx server instance that has been paired
// with this authority via the ServerPairingService.
type Server struct {
	URN          URN
	Name         string
	Endpoint     string
	CertPEM      []byte
	CertSerial   string
	Revoked      bool
	RegisteredAt time.Time
	ExpiresAt    time.Time
	LastSeenAt   time.Time
}
