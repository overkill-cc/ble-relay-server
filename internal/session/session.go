package session

import (
	"sync"
	"time"
)

// HostState tracks whether a session's host connection is live, in its
// reconnect grace period, or gone for good.
type HostState int

const (
	HostConnected HostState = iota
	HostInGracePeriod
	HostGone
)

// Conn is the minimal send/close surface a transport (WebSocket) connection
// must provide. Kept transport-agnostic so this package has no dependency on
// any specific WebSocket library.
type Conn interface {
	// Send enqueues a raw frame for delivery; must not block the caller for
	// long (implementations should buffer internally).
	Send(frame []byte)
	// Close terminates the connection.
	Close(reason string)
}

// Session represents one host's shared access grant: a username+password
// pair covering every BLE device that host currently bridges.
type Session struct {
	mu sync.Mutex

	Username     string
	passwordHash string
	passwordSalt string

	Host      Conn
	HostState HostState

	Clients map[string]Conn // clientId -> connection

	CreatedAt     time.Time
	LastActivity  time.Time
	graceDeadline time.Time
}

func newSession(username, passwordHash, passwordSalt string) *Session {
	now := time.Now()
	return &Session{
		Username:     username,
		passwordHash: passwordHash,
		passwordSalt: passwordSalt,
		HostState:    HostConnected,
		Clients:      make(map[string]Conn),
		CreatedAt:    now,
		LastActivity: now,
	}
}

// VerifyPassword checks a plaintext password against this session's stored
// hash. Used for client auth, where a human typed the plaintext in.
func (s *Session) VerifyPassword(password string) bool {
	s.mu.Lock()
	hash, salt := s.passwordHash, s.passwordSalt
	s.mu.Unlock()
	return VerifyPassword(password, hash, salt)
}

// VerifyHash checks an already-hashed credential against this session's
// stored hash. Used for host resume, where the host never re-transmits the
// plaintext password at all — only what it originally registered with.
func (s *Session) VerifyHash(hash, salt string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return subtleConstantTimeEqual(hash, s.passwordHash) && subtleConstantTimeEqual(salt, s.passwordSalt)
}

// AddClient registers a newly authenticated client connection.
func (s *Session) AddClient(clientID string, conn Conn) {
	s.mu.Lock()
	s.Clients[clientID] = conn
	s.LastActivity = time.Now()
	s.mu.Unlock()
}

// RemoveClient drops a client connection (on disconnect).
func (s *Session) RemoveClient(clientID string) {
	s.mu.Lock()
	delete(s.Clients, clientID)
	s.LastActivity = time.Now()
	s.mu.Unlock()
}

// ClientIDs returns a snapshot of currently connected client ids.
func (s *Session) ClientIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.Clients))
	for id := range s.Clients {
		ids = append(ids, id)
	}
	return ids
}

// SendToHost delivers a raw frame to the host connection, if any.
func (s *Session) SendToHost(frame []byte) {
	s.mu.Lock()
	host := s.Host
	s.mu.Unlock()
	if host != nil {
		host.Send(frame)
	}
}

// SendToClient delivers a raw frame to one specific client.
func (s *Session) SendToClient(clientID string, frame []byte) bool {
	s.mu.Lock()
	c, ok := s.Clients[clientID]
	s.mu.Unlock()
	if !ok {
		return false
	}
	c.Send(frame)
	return true
}

// BroadcastToClients delivers a raw frame to every connected client.
func (s *Session) BroadcastToClients(frame []byte) {
	s.mu.Lock()
	conns := make([]Conn, 0, len(s.Clients))
	for _, c := range s.Clients {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.Send(frame)
	}
}

// MarkHostGoneWithGrace transitions the session into its reconnect grace
// period and returns the deadline by which the host must resume.
func (s *Session) MarkHostGoneWithGrace(grace time.Duration) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.HostState = HostInGracePeriod
	s.Host = nil
	s.graceDeadline = time.Now().Add(grace)
	return s.graceDeadline
}

// GraceExpired reports whether the session's reconnect grace period has passed.
func (s *Session) GraceExpired(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.HostState == HostInGracePeriod && now.After(s.graceDeadline)
}

// Resume reattaches a host connection after a transient disconnect.
func (s *Session) Resume(conn Conn) {
	s.mu.Lock()
	s.Host = conn
	s.HostState = HostConnected
	s.LastActivity = time.Now()
	s.mu.Unlock()
}

// IdleSince reports whether the session has seen no activity since the given deadline.
func (s *Session) IdleSince(deadline time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.LastActivity.Before(deadline) && len(s.Clients) == 0
}

// IsEmpty reports whether the session has no host and no clients left (safe to reap).
func (s *Session) IsEmpty() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.HostState == HostGone && len(s.Clients) == 0
}
