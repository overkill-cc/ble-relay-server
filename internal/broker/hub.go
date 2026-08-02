package broker

import "github.com/overkill-cc/ble-relay-server/internal/session"

// RouteFromHost forwards a frame the host sent onward to the relevant
// client(s): a specific client if the envelope names one, otherwise every
// client currently in the session (used for e.g. device_list/gatt_tree/
// value_changed broadcasts).
func RouteFromHost(sess *session.Session, raw []byte) {
	env, err := Unmarshal(raw)
	if err != nil {
		return
	}
	if env.ClientID == "" {
		sess.BroadcastToClients(raw)
		return
	}
	sess.SendToClient(env.ClientID, raw)
}

// RouteFromClient forwards a frame from a specific client to the host,
// stamping the envelope's clientId to the authenticated connection's id so a
// client can never spoof requests as coming from another client.
func RouteFromClient(sess *session.Session, clientID string, raw []byte) {
	env, err := Unmarshal(raw)
	if err != nil {
		return
	}
	env.ClientID = clientID
	sess.SendToHost(Marshal(env))
}
