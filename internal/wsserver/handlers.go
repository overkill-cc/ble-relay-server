package wsserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"time"

	"nhooyr.io/websocket"

	"github.com/overkill-cc/ble-relay-server/internal/broker"
	"github.com/overkill-cc/ble-relay-server/internal/ratelimit"
	"github.com/overkill-cc/ble-relay-server/internal/session"
)

const (
	firstFrameTimeout = 10 * time.Second
	hostGracePeriod   = 60 * time.Second
	idleTimeout       = 30 * time.Minute
	reapInterval      = 30 * time.Second
	// readIdleTimeout bounds how long a read loop will block waiting for the
	// next frame. The Dart client pings every 20s, so 60s of total silence
	// means the peer is gone (radio suspended, NAT mapping expired, process
	// killed) even though the OS hasn't reported the TCP connection as
	// closed yet — without this, a dead host connection would sit in the
	// registry as "connected" indefinitely, blocking any real reconnect.
	readIdleTimeout = 60 * time.Second
)

// Server wires the relay's HTTP/WebSocket endpoints to the session registry
// and broker. It knows the wire protocol's control-message shapes; anything
// past the handshake is routed opaquely by the broker package.
type Server struct {
	registry *session.Registry
	limiter  *ratelimit.Limiter
}

func NewServer() *Server {
	return &Server{
		registry: session.NewRegistry(),
		// 5 failed attempts per minute per IP, then a 5 minute cooldown.
		limiter: ratelimit.New(5, time.Minute, 5*time.Minute),
	}
}

func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/host", s.HandleHost)
	mux.HandleFunc("/ws/client", s.HandleClient)
	mux.HandleFunc("/healthz", s.HandleHealth)
	return mux
}

func (s *Server) HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// StartReaper runs the idle/grace-expiry sweep until ctx is cancelled.
func (s *Server) StartReaper(ctx context.Context) {
	ticker := time.NewTicker(reapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reaped := s.registry.ReapExpired(time.Now(), idleTimeout)
			for _, username := range reaped {
				log.Printf("relay: reaped session %q", username)
			}
		}
	}
}

func acceptWS(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	return websocket.Accept(w, r, &websocket.AcceptOptions{
		// Native app clients (not browsers) typically don't send an Origin
		// header at all; when they do (e.g. a desktop WebView), we don't
		// know their origin ahead of time, so origin checking is left to
		// network-level access control (this relay is meant to sit behind
		// the operator's own TLS-terminating reverse proxy / firewall).
		InsecureSkipVerify: true,
	})
}

func readJSON(ctx context.Context, ws *websocket.Conn) (broker.Envelope, error) {
	_, data, err := ws.Read(ctx)
	if err != nil {
		return broker.Envelope{}, err
	}
	return broker.Unmarshal(data)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func randomID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// HandleHost accepts a host connection, processes the register_session or
// resume_session handshake, then relays frames until the connection drops.
func (s *Server) HandleHost(w http.ResponseWriter, r *http.Request) {
	ws, err := acceptWS(w, r)
	if err != nil {
		return
	}
	defer ws.Close(websocket.StatusInternalError, "closed")

	handshakeCtx, cancel := context.WithTimeout(r.Context(), firstFrameTimeout)
	env, err := readJSON(handshakeCtx, ws)
	cancel()
	if err != nil {
		return
	}

	conn := newWSConn("host:pending", ws)

	var sess *session.Session
	var username string

	switch env.Type {
	case broker.TypeRegisterSession:
		var p registerSessionPayload
		if jsonErr := json.Unmarshal(env.Payload, &p); jsonErr != nil {
			conn.Send(broker.NewError(env.ID, "", "internal_error", "malformed register_session"))
			return
		}
		sess, username = s.registry.RegisterHost(p.DesiredUsername, p.PasswordHash, p.PasswordSalt, conn)
		conn.id = "host:" + username
		ackPayload, _ := json.Marshal(sessionRegisteredPayload{Username: username})
		conn.Send(broker.Marshal(broker.Envelope{ID: env.ID, Type: broker.TypeSessionRegistered, Payload: ackPayload}))
		log.Printf("relay: host registered session %q", username)

	case broker.TypeResumeSession:
		var p resumeSessionPayload
		if jsonErr := json.Unmarshal(env.Payload, &p); jsonErr != nil {
			conn.Send(broker.NewError(env.ID, "", "internal_error", "malformed resume_session"))
			return
		}
		sess, err = s.registry.ResumeHost(p.Username, p.PasswordHash, p.PasswordSalt, conn)
		if err != nil {
			conn.Send(broker.NewError(env.ID, "", "invalid_credentials", err.Error()))
			return
		}
		username = p.Username
		conn.id = "host:" + username
		ackPayload, _ := json.Marshal(sessionResumedPayload{OK: true, PendingClients: sess.ClientIDs()})
		conn.Send(broker.Marshal(broker.Envelope{ID: env.ID, Type: broker.TypeSessionResumed, Payload: ackPayload}))
		sess.BroadcastToClients(broker.Marshal(broker.Envelope{Type: broker.TypeHostReconnected}))
		log.Printf("relay: host resumed session %q", username)

	default:
		conn.Send(broker.NewError(env.ID, "", "internal_error", "expected register_session or resume_session"))
		return
	}

	s.hostReadLoop(r.Context(), ws, conn, sess, username)
}

func (s *Server) hostReadLoop(ctx context.Context, ws *websocket.Conn, conn *wsConn, sess *session.Session, username string) {
	for {
		readCtx, cancel := context.WithTimeout(ctx, readIdleTimeout)
		_, data, err := ws.Read(readCtx)
		cancel()
		if err != nil {
			break
		}
		env, err := broker.Unmarshal(data)
		if err != nil {
			continue
		}
		switch env.Type {
		case broker.TypePing:
			conn.Send(broker.Marshal(broker.Envelope{Type: broker.TypePong}))
		default:
			broker.RouteFromHost(sess, data)
		}
	}

	conn.Close("host connection closed")
	deadline := sess.MarkHostGoneWithGrace(hostGracePeriod)
	graceSeconds := int(time.Until(deadline).Seconds())
	sess.BroadcastToClients(broker.Marshal(broker.Envelope{
		Type:    broker.TypeHostDisconnected,
		Payload: mustJSON(hostDisconnectedPayload{GraceSeconds: graceSeconds}),
	}))
	log.Printf("relay: host disconnected from session %q (grace %ds)", username, graceSeconds)
}

// HandleClient accepts a client connection, processes the auth handshake
// against a session's stored credentials, then relays frames until the
// connection drops.
func (s *Server) HandleClient(w http.ResponseWriter, r *http.Request) {
	ws, err := acceptWS(w, r)
	if err != nil {
		return
	}
	defer ws.Close(websocket.StatusInternalError, "closed")

	ip := clientIP(r)
	if ok, retryAfter := s.limiter.Allow(ip); !ok {
		payload, _ := json.Marshal(authErrorPayload{Reason: "rate_limited", RetryAfterSeconds: retryAfter})
		_ = writeEnvelope(r.Context(), ws, broker.Envelope{Type: broker.TypeAuthError, Payload: payload})
		return
	}

	handshakeCtx, cancel := context.WithTimeout(r.Context(), firstFrameTimeout)
	env, err := readJSON(handshakeCtx, ws)
	cancel()
	if err != nil || env.Type != broker.TypeAuth {
		return
	}

	var p authPayload
	if jsonErr := json.Unmarshal(env.Payload, &p); jsonErr != nil {
		return
	}

	sess, authErr := s.registry.AuthenticateClient(p.Username, p.Password)
	if authErr != nil {
		s.limiter.RecordFailure(ip)
		payload, _ := json.Marshal(authErrorPayload{Reason: "invalid_credentials"})
		_ = writeEnvelope(r.Context(), ws, broker.Envelope{ID: env.ID, Type: broker.TypeAuthError, Payload: payload})
		return
	}
	s.limiter.RecordSuccess(ip)

	clientID := randomID()
	conn := newWSConn("client:"+clientID, ws)
	sess.AddClient(clientID, conn)

	okPayload, _ := json.Marshal(authOKPayload{SessionID: sess.Username, HostOnline: sess.HostState == session.HostConnected})
	conn.Send(broker.Marshal(broker.Envelope{ID: env.ID, Type: broker.TypeAuthOK, ClientID: clientID, Payload: okPayload}))
	sess.SendToHost(broker.Marshal(broker.Envelope{
		Type:     broker.TypeClientJoined,
		ClientID: clientID,
		Payload:  mustJSON(clientJoinedPayload{ClientID: clientID}),
	}))
	log.Printf("relay: client %s joined session %q", clientID, sess.Username)

	s.clientReadLoop(r.Context(), ws, conn, sess, clientID)
}

func (s *Server) clientReadLoop(ctx context.Context, ws *websocket.Conn, conn *wsConn, sess *session.Session, clientID string) {
	for {
		readCtx, cancel := context.WithTimeout(ctx, readIdleTimeout)
		_, data, err := ws.Read(readCtx)
		cancel()
		if err != nil {
			break
		}
		env, err := broker.Unmarshal(data)
		if err != nil {
			continue
		}
		switch env.Type {
		case broker.TypePing:
			conn.Send(broker.Marshal(broker.Envelope{Type: broker.TypePong}))
		default:
			broker.RouteFromClient(sess, clientID, data)
		}
	}

	conn.Close("client connection closed")
	sess.RemoveClient(clientID)
	sess.SendToHost(broker.Marshal(broker.Envelope{
		Type:     broker.TypeClientLeft,
		ClientID: clientID,
		Payload:  mustJSON(clientLeftPayload{ClientID: clientID, Reason: "disconnect"}),
	}))
	log.Printf("relay: client %s left session %q", clientID, sess.Username)
}

func writeEnvelope(ctx context.Context, ws *websocket.Conn, env broker.Envelope) error {
	return ws.Write(ctx, websocket.MessageText, broker.Marshal(env))
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
