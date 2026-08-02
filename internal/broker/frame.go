// Package broker implements the session-scoped fan-out between one host
// WebSocket connection and N client WebSocket connections. It understands
// only the envelope shape of the wire protocol (routing fields) and treats
// payload as opaque — GATT semantics live entirely in the Flutter app.
package broker

import (
	"encoding/json"
	"time"
)

const ProtocolVersion = 1

// Envelope is the common wire-protocol frame shape shared by every message
// type (see the project plan's "Wire Protocol" section for the full catalog).
type Envelope struct {
	V        int             `json:"v"`
	ID       string          `json:"id,omitempty"`
	Type     string          `json:"type"`
	TS       int64           `json:"ts,omitempty"`
	ClientID string          `json:"clientId,omitempty"`
	DeviceID string          `json:"deviceId,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

// Control message types the relay itself interprets (session/auth handshake).
// Everything else is routed opaquely between host and client(s).
const (
	TypeRegisterSession   = "register_session"
	TypeSessionRegistered = "session_registered"
	TypeResumeSession     = "resume_session"
	TypeSessionResumed    = "session_resumed"
	TypeAuth              = "auth"
	TypeAuthOK            = "auth_ok"
	TypeAuthError         = "auth_error"
	TypeClientJoined      = "client_joined"
	TypeClientLeft        = "client_left"
	TypeHostDisconnected  = "host_disconnected"
	TypeHostReconnected   = "host_reconnected"
	TypePing              = "ping"
	TypePong              = "pong"
	TypeError             = "error"
)

func Marshal(env Envelope) []byte {
	env.V = ProtocolVersion
	if env.TS == 0 {
		env.TS = time.Now().UnixMilli()
	}
	b, err := json.Marshal(env)
	if err != nil {
		// Envelope only contains JSON-safe primitive/raw-message fields, so
		// marshaling cannot fail in practice.
		panic(err)
	}
	return b
}

func Unmarshal(raw []byte) (Envelope, error) {
	var env Envelope
	err := json.Unmarshal(raw, &env)
	return env, err
}

func NewError(id, clientID, code, message string) []byte {
	payload, _ := json.Marshal(map[string]string{"code": code, "message": message})
	return Marshal(Envelope{ID: id, Type: TypeError, ClientID: clientID, Payload: payload})
}
