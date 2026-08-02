package wsserver

// Payload shapes for the control messages the relay itself interprets.
// These mirror the "Wire Protocol" section of the project plan.

type registerSessionPayload struct {
	DesiredUsername string `json:"desiredUsername"`
	PasswordHash    string `json:"passwordHash"`
	PasswordSalt    string `json:"passwordSalt"`
}

type sessionRegisteredPayload struct {
	Username string `json:"username"`
}

type resumeSessionPayload struct {
	Username     string `json:"username"`
	PasswordHash string `json:"passwordHash"`
	PasswordSalt string `json:"passwordSalt"`
}

type sessionResumedPayload struct {
	OK             bool     `json:"ok"`
	PendingClients []string `json:"pendingClients"`
}

type authPayload struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authOKPayload struct {
	SessionID  string `json:"sessionId"`
	HostOnline bool   `json:"hostOnline"`
}

type authErrorPayload struct {
	Reason            string `json:"reason"`
	RetryAfterSeconds int    `json:"retryAfterSeconds,omitempty"`
}

type clientJoinedPayload struct {
	ClientID string `json:"clientId"`
}

type clientLeftPayload struct {
	ClientID string `json:"clientId"`
	Reason   string `json:"reason"`
}

type hostDisconnectedPayload struct {
	GraceSeconds int `json:"graceSeconds"`
}
