// Package blerelay holds assets served by the relay server.
//
// The privacy policy sits at the repository root because it is the project's
// public, human-facing document rather than an implementation detail of the
// binary. go:embed cannot reach outside its own package directory, so the
// embed is declared here instead of in cmd/relayd.
package blerelay

import _ "embed"

// PrivacyPolicyHTML is served as the app's public privacy-policy URL, which
// the Play Store listing points at.
//
//go:embed privacy_policy.html
var PrivacyPolicyHTML []byte
