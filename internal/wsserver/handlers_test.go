package wsserver

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/overkill-cc/ble-relay-server/internal/broker"
	"github.com/overkill-cc/ble-relay-server/internal/session"
)

// TestEndToEndHostClientRelay exercises the full handshake + relay path:
// a host registers a session, a client authenticates and joins, then a
// read_request/read_response round-trips through the broker exactly as a
// real GATT read would.
func TestEndToEndHostClientRelay(t *testing.T) {
	srv := NewServer(nil)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	wsBase := "ws" + strings.TrimPrefix(ts.URL, "http")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	hostConn, _, err := websocket.Dial(ctx, wsBase+"/ws/host", nil)
	if err != nil {
		t.Fatalf("dial host: %v", err)
	}
	defer hostConn.Close(websocket.StatusNormalClosure, "")

	hash, salt, err := session.HashPassword("secret123")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	regPayload, _ := json.Marshal(registerSessionPayload{
		DesiredUsername: "TestDevice",
		PasswordHash:    hash,
		PasswordSalt:    salt,
	})
	if err := writeEnvelope(ctx, hostConn, broker.Envelope{ID: "r1", Type: broker.TypeRegisterSession, Payload: regPayload}); err != nil {
		t.Fatalf("send register_session: %v", err)
	}

	regAck, err := readJSON(ctx, hostConn)
	if err != nil {
		t.Fatalf("read session_registered: %v", err)
	}
	if regAck.Type != broker.TypeSessionRegistered {
		t.Fatalf("expected session_registered, got %s", regAck.Type)
	}
	var regAckPayload sessionRegisteredPayload
	_ = json.Unmarshal(regAck.Payload, &regAckPayload)
	if regAckPayload.Username != "TestDevice" {
		t.Fatalf("expected username TestDevice, got %s", regAckPayload.Username)
	}

	clientConn, _, err := websocket.Dial(ctx, wsBase+"/ws/client", nil)
	if err != nil {
		t.Fatalf("dial client: %v", err)
	}
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	authPayloadBytes, _ := json.Marshal(authPayload{Username: "TestDevice", Password: "secret123"})
	if err := writeEnvelope(ctx, clientConn, broker.Envelope{ID: "a1", Type: broker.TypeAuth, Payload: authPayloadBytes}); err != nil {
		t.Fatalf("send auth: %v", err)
	}

	authAck, err := readJSON(ctx, clientConn)
	if err != nil {
		t.Fatalf("read auth_ok: %v", err)
	}
	if authAck.Type != broker.TypeAuthOK {
		t.Fatalf("expected auth_ok, got %s (payload %s)", authAck.Type, authAck.Payload)
	}

	hostSideJoin, err := readJSON(ctx, hostConn)
	if err != nil {
		t.Fatalf("read client_joined: %v", err)
	}
	if hostSideJoin.Type != broker.TypeClientJoined {
		t.Fatalf("expected client_joined, got %s", hostSideJoin.Type)
	}

	readReqPayload, _ := json.Marshal(map[string]any{"handle": 42})
	if err := writeEnvelope(ctx, clientConn, broker.Envelope{
		ID: "req-1", Type: "read_request", DeviceID: "dev1", Payload: readReqPayload,
	}); err != nil {
		t.Fatalf("send read_request: %v", err)
	}

	hostSideReq, err := readJSON(ctx, hostConn)
	if err != nil {
		t.Fatalf("host read read_request: %v", err)
	}
	if hostSideReq.Type != "read_request" || hostSideReq.DeviceID != "dev1" {
		t.Fatalf("unexpected frame at host: %+v", hostSideReq)
	}
	if hostSideReq.ClientID == "" {
		t.Fatalf("expected relay to stamp clientId onto host-bound frame")
	}

	readRespPayload, _ := json.Marshal(map[string]any{"handle": 42, "ok": true, "value": "AQID"})
	if err := writeEnvelope(ctx, hostConn, broker.Envelope{
		ID: "req-1", Type: "read_response", ClientID: hostSideReq.ClientID, DeviceID: "dev1", Payload: readRespPayload,
	}); err != nil {
		t.Fatalf("send read_response: %v", err)
	}

	clientSideResp, err := readJSON(ctx, clientConn)
	if err != nil {
		t.Fatalf("client read read_response: %v", err)
	}
	if clientSideResp.Type != "read_response" {
		t.Fatalf("expected read_response, got %s", clientSideResp.Type)
	}
	var respPayload map[string]any
	_ = json.Unmarshal(clientSideResp.Payload, &respPayload)
	if respPayload["value"] != "AQID" {
		t.Fatalf("expected relayed value AQID, got %v", respPayload["value"])
	}
}

// TestUsernameCollisionSuffixing verifies two hosts registering the same
// desired name get suffixed usernames.
func TestUsernameCollisionSuffixing(t *testing.T) {
	srv := NewServer(nil)
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	wsBase := "ws" + strings.TrimPrefix(ts.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	register := func() string {
		conn, _, err := websocket.Dial(ctx, wsBase+"/ws/host", nil)
		if err != nil {
			t.Fatalf("dial host: %v", err)
		}
		t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })

		hash, salt, _ := session.HashPassword("pw")
		payload, _ := json.Marshal(registerSessionPayload{DesiredUsername: "SmartLock", PasswordHash: hash, PasswordSalt: salt})
		if err := writeEnvelope(ctx, conn, broker.Envelope{ID: "r", Type: broker.TypeRegisterSession, Payload: payload}); err != nil {
			t.Fatalf("send register_session: %v", err)
		}
		ack, err := readJSON(ctx, conn)
		if err != nil {
			t.Fatalf("read ack: %v", err)
		}
		var p sessionRegisteredPayload
		_ = json.Unmarshal(ack.Payload, &p)
		return p.Username
	}

	first := register()
	second := register()
	if first != "SmartLock" {
		t.Fatalf("expected first username SmartLock, got %s", first)
	}
	if second != "SmartLock2" {
		t.Fatalf("expected second username SmartLock2, got %s", second)
	}
}
