package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// Browsers apply form-action to the redirect that follows the form post, so the
// OAuth callback's origin must be allowed or the sign-in is blocked after approval.
func TestLoginCSPAllowsRedirectOrigin(t *testing.T) {
	w := httptest.NewRecorder()
	(&AuthServer{}).renderLogin(w, authzParams{
		ClientName:  "Claude",
		RedirectURI: "https://claude.ai/api/mcp/auth_callback",
	})

	csp := w.Header().Get("Content-Security-Policy")
	var formAction string
	for _, directive := range strings.Split(csp, ";") {
		directive = strings.TrimSpace(directive)
		if strings.HasPrefix(directive, "form-action ") {
			formAction = directive
		}
	}
	want := "form-action 'self' https://claude.ai"
	if formAction != want {
		t.Fatalf("form-action = %q, want %q (full CSP: %q)", formAction, want, csp)
	}
}
