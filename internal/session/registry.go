package session

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrNoSuchSession     = errors.New("no such session")
	ErrInvalidCredential = errors.New("invalid credentials")
	ErrHostAlreadyLive   = errors.New("host already connected")
)

// Registry is the in-memory, mutex-guarded map of active sessions.
type Registry struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewRegistry() *Registry {
	return &Registry{sessions: make(map[string]*Session)}
}

// RegisterHost atomically picks a free username (suffixing on collision),
// creates a new session, and attaches the host connection. Returns the final
// username actually assigned.
func (r *Registry) RegisterHost(desiredUsername, passwordHash, passwordSalt string, conn Conn) (*Session, string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	username := r.reserveUsernameLocked(desiredUsername)
	sess := newSession(username, passwordHash, passwordSalt)
	sess.Host = conn
	r.sessions[username] = sess
	return sess, username
}

// reserveUsernameLocked must be called with r.mu held.
func (r *Registry) reserveUsernameLocked(desired string) string {
	if _, taken := r.sessions[desired]; !taken {
		return desired
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s%d", desired, n)
		if _, taken := r.sessions[candidate]; !taken {
			return candidate
		}
	}
}

// ResumeHost reattaches a host to an existing session within its grace
// period. The host never re-transmits its plaintext password — only the
// same hash+salt it originally registered with — so this is a hash equality
// check, not a VerifyPassword call.
func (r *Registry) ResumeHost(username, passwordHash, passwordSalt string, conn Conn) (*Session, error) {
	r.mu.Lock()
	sess, ok := r.sessions[username]
	r.mu.Unlock()
	if !ok {
		return nil, ErrNoSuchSession
	}
	if !sess.VerifyHash(passwordHash, passwordSalt) {
		return nil, ErrInvalidCredential
	}
	if sess.HostState == HostConnected {
		return nil, ErrHostAlreadyLive
	}
	sess.Resume(conn)
	return sess, nil
}

// AuthenticateClient looks up a session by username and verifies the
// password. Never distinguishes "no such session" from "wrong password" in
// what gets returned to the caller for auth purposes beyond the sentinel
// error, so handlers can respond with a single generic reason.
func (r *Registry) AuthenticateClient(username, password string) (*Session, error) {
	r.mu.Lock()
	sess, ok := r.sessions[username]
	r.mu.Unlock()
	if !ok || !sess.VerifyPassword(password) {
		return nil, ErrInvalidCredential
	}
	return sess, nil
}

// Get returns a session by username, if it exists.
func (r *Registry) Get(username string) (*Session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[username]
	return s, ok
}

// StartHostGrace transitions a session into its reconnect grace window.
func (r *Registry) StartHostGrace(username string, grace time.Duration) {
	r.mu.Lock()
	sess, ok := r.sessions[username]
	r.mu.Unlock()
	if ok {
		sess.MarkHostGoneWithGrace(grace)
	}
}

// ReapExpired removes sessions whose host never resumed within its grace
// period (transitioning them to fully gone and evicting once also clientless)
// and sessions that have been idle with nobody connected. Should be called
// periodically from a background goroutine.
func (r *Registry) ReapExpired(now time.Time, idleTimeout time.Duration) []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	var reaped []string
	for username, sess := range r.sessions {
		if sess.GraceExpired(now) {
			sess.mu.Lock()
			sess.HostState = HostGone
			sess.mu.Unlock()
		}
		if sess.IsEmpty() || sess.IdleSince(now.Add(-idleTimeout)) {
			delete(r.sessions, username)
			reaped = append(reaped, username)
		}
	}
	return reaped
}

// Count returns the number of active sessions (for diagnostics).
func (r *Registry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sessions)
}
