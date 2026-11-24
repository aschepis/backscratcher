package main

// A minimal single-user OAuth 2.1 authorization server, enough for Claude's
// custom connectors (dynamic client registration + authorization code with
// PKCE + refresh tokens). The "user" signs in with a passphrase set via
// `imessage-mcp set-password`.
//
// Tokens are random 256-bit values; only their SHA-256 hashes are stored.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/crypto/bcrypt"
)

const (
	accessTTL  = 24 * time.Hour
	refreshTTL = 60 * 24 * time.Hour
	codeTTL    = 2 * time.Minute
	scope      = "messages:read"
)

type tokenRec struct {
	Kind     string    `json:"kind"` // access | refresh | static
	ClientID string    `json:"client_id,omitempty"`
	Label    string    `json:"label,omitempty"`
	Expires  time.Time `json:"expires,omitempty"`
}

type clientRec struct {
	Name         string    `json:"name"`
	RedirectURIs []string  `json:"redirect_uris"`
	Created      time.Time `json:"created"`
}

type authCode struct {
	clientID, redirectURI, challenge string
	expires                          time.Time
}

// State is persisted to state.json (mode 0600).
type State struct {
	PasswordHash string                `json:"password_hash"`
	Clients      map[string]*clientRec `json:"clients"`
	Tokens       map[string]*tokenRec  `json:"tokens"` // sha256(token) → record
}

type AuthServer struct {
	mu      sync.Mutex
	path    string
	st      State
	codes   map[string]*authCode
	issuer  string // public origin, no trailing slash
	allowed []string

	failMu   sync.Mutex
	failures []time.Time
}

func loadAuth(dir, issuer string, allowedHosts []string) (*AuthServer, error) {
	a := &AuthServer{
		path:    filepath.Join(dir, "state.json"),
		codes:   map[string]*authCode{},
		issuer:  strings.TrimRight(issuer, "/"),
		allowed: allowedHosts,
	}
	b, err := os.ReadFile(a.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &a.st); err != nil {
			return nil, fmt.Errorf("parse %s: %w", a.path, err)
		}
	}
	if a.st.Clients == nil {
		a.st.Clients = map[string]*clientRec{}
	}
	if a.st.Tokens == nil {
		a.st.Tokens = map[string]*tokenRec{}
	}
	return a, nil
}

// saveLocked writes state atomically. Caller holds a.mu.
func (a *AuthServer) saveLocked() error {
	now := time.Now()
	for k, t := range a.st.Tokens {
		if !t.Expires.IsZero() && now.After(t.Expires) {
			delete(a.st.Tokens, k)
		}
	}
	b, err := json.MarshalIndent(a.st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0o700); err != nil {
		return err
	}
	tmp := a.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, a.path)
}

func (a *AuthServer) SetPassword(pw string) error {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.st.PasswordHash = string(h)
	// A new password signs out every existing OAuth session.
	for k, t := range a.st.Tokens {
		if t.Kind != "static" {
			delete(a.st.Tokens, k)
		}
	}
	return a.saveLocked()
}

func (a *AuthServer) HasPassword() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.st.PasswordHash != ""
}

// NewStaticToken creates a non-expiring bearer token (for Claude Code,
// scripts, curl). It is printed once and never stored in plain text.
func (a *AuthServer) NewStaticToken(label string) (string, error) {
	tok := randToken()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.st.Tokens[hashToken(tok)] = &tokenRec{Kind: "static", Label: label}
	return tok, a.saveLocked()
}

func (a *AuthServer) RevokeAll() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.st.Tokens = map[string]*tokenRec{}
	a.st.Clients = map[string]*clientRec{}
	return a.saveLocked()
}

// Verify implements auth.TokenVerifier.
func (a *AuthServer) Verify(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rec := a.st.Tokens[hashToken(token)]
	if rec == nil || (rec.Kind != "access" && rec.Kind != "static") {
		return nil, auth.ErrInvalidToken
	}
	exp := rec.Expires
	if rec.Kind == "static" {
		exp = time.Now().Add(365 * 24 * time.Hour)
	} else if time.Now().After(exp) {
		return nil, auth.ErrInvalidToken
	}
	return &auth.TokenInfo{Scopes: []string{scope}, Expiration: exp, UserID: "owner"}, nil
}

// ---------------------------------------------------------------------------
// HTTP endpoints

func (a *AuthServer) Register(mux *http.ServeMux) {
	mux.Handle("GET /.well-known/oauth-protected-resource", a.resourceMetadata())
	mux.Handle("GET /.well-known/oauth-protected-resource/mcp", a.resourceMetadata())
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", a.serverMetadata)
	mux.HandleFunc("POST /register", a.register)
	mux.HandleFunc("GET /authorize", a.authorizeForm)
	mux.HandleFunc("POST /authorize", a.authorizeSubmit)
	mux.HandleFunc("POST /token", a.token)
}

func (a *AuthServer) ResourceMetadataURL() string {
	return a.issuer + "/.well-known/oauth-protected-resource/mcp"
}

func (a *AuthServer) resourceMetadata() http.Handler {
	return auth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource:               a.issuer + "/mcp",
		AuthorizationServers:   []string{a.issuer},
		ScopesSupported:        []string{scope},
		BearerMethodsSupported: []string{"header"},
	})
}

func (a *AuthServer) serverMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                a.issuer,
		"authorization_endpoint":                a.issuer + "/authorize",
		"token_endpoint":                        a.issuer + "/token",
		"registration_endpoint":                 a.issuer + "/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{scope},
	})
}

func (a *AuthServer) redirectAllowed(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Fragment != "" {
		return false
	}
	host := u.Hostname()
	loopback := host == "localhost" || net.ParseIP(host).IsLoopback()
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return false
	}
	for _, h := range a.allowed {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

func (a *AuthServer) register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "bad JSON")
		return
	}
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > 10 {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris required")
		return
	}
	for _, u := range req.RedirectURIs {
		if !a.redirectAllowed(u) {
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect host not allowed: "+u+" (see allowed_redirect_hosts in config.json)")
			return
		}
	}
	id := randToken()[:24]
	a.mu.Lock()
	if len(a.st.Clients) > 100 { // keep the file small; drop the oldest
		var oldest string
		for k, c := range a.st.Clients {
			if oldest == "" || c.Created.Before(a.st.Clients[oldest].Created) {
				oldest = k
			}
		}
		delete(a.st.Clients, oldest)
	}
	a.st.Clients[id] = &clientRec{Name: truncate(req.ClientName, 100), RedirectURIs: req.RedirectURIs, Created: time.Now()}
	err := a.saveLocked()
	a.mu.Unlock()
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "could not save client")
		return
	}
	slog.Info("oauth client registered", "client", req.ClientName)
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  id,
		"client_name":                req.ClientName,
		"redirect_uris":              req.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
}

type authzParams struct {
	ClientID, ClientName, RedirectURI, State, Challenge, Error string
}

// validateAuthz checks the request parameters. Errors before the redirect_uri
// is validated are shown on the page, never redirected.
func (a *AuthServer) validateAuthz(v url.Values) (authzParams, error) {
	p := authzParams{
		ClientID:    v.Get("client_id"),
		RedirectURI: v.Get("redirect_uri"),
		State:       v.Get("state"),
		Challenge:   v.Get("code_challenge"),
	}
	a.mu.Lock()
	c := a.st.Clients[p.ClientID]
	a.mu.Unlock()
	if c == nil {
		return p, errors.New("unknown client_id; remove and re-add the connector")
	}
	p.ClientName = c.Name
	if !contains(c.RedirectURIs, p.RedirectURI) {
		return p, errors.New("redirect_uri does not match the registered client")
	}
	if v.Get("response_type") != "code" {
		return p, errors.New("response_type must be code")
	}
	if p.Challenge == "" || v.Get("code_challenge_method") != "S256" {
		return p, errors.New("PKCE with S256 is required")
	}
	return p, nil
}

var loginPage = template.Must(template.New("login").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>iMessage MCP sign-in</title>
<style>
body{font:16px -apple-system,system-ui,sans-serif;background:#f5f5f7;color:#1d1d1f;display:flex;min-height:100vh;align-items:center;justify-content:center;margin:0;padding:16px}
form{background:#fff;padding:28px;border-radius:14px;box-shadow:0 2px 12px #0001;max-width:360px;width:100%}
h1{font-size:20px;margin:0 0 8px}p{color:#555;margin:0 0 18px;font-size:14px}
input[type=password]{width:100%;box-sizing:border-box;padding:10px;font-size:16px;border:1px solid #ccc;border-radius:8px}
button{margin-top:14px;width:100%;padding:10px;font-size:16px;border:0;border-radius:8px;background:#0071e3;color:#fff}
.err{color:#c00;margin-bottom:12px;font-size:14px}
@media (prefers-color-scheme:dark){body{background:#000;color:#f5f5f7}form{background:#1c1c1e}p{color:#aaa}input[type=password]{background:#2c2c2e;color:#fff;border-color:#444}}
</style></head><body>
<form method="post" action="/authorize">
<h1>Allow access to your messages?</h1>
<p><b>{{.ClientName}}</b> wants read-only access to your iMessage history.</p>
{{if .Error}}<div class="err">{{.Error}}</div>{{end}}
<input type="password" name="password" placeholder="Passphrase" autofocus required autocomplete="current-password">
<input type="hidden" name="client_id" value="{{.ClientID}}">
<input type="hidden" name="redirect_uri" value="{{.RedirectURI}}">
<input type="hidden" name="state" value="{{.State}}">
<input type="hidden" name="code_challenge" value="{{.Challenge}}">
<input type="hidden" name="code_challenge_method" value="S256">
<input type="hidden" name="response_type" value="code">
<button type="submit">Allow</button>
</form></body></html>`))

func (a *AuthServer) renderLogin(w http.ResponseWriter, p authzParams) {
	formAction := "'self'"
	// Browsers apply form-action to the post-approval redirect, so the validated callback origin must be allowed.
	if u, err := url.Parse(p.RedirectURI); err == nil && u.Scheme != "" && u.Host != "" {
		formAction += " " + u.Scheme + "://" + u.Host
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action "+formAction+"; frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Cache-Control", "no-store")
	loginPage.Execute(w, p)
}

func (a *AuthServer) authorizeForm(w http.ResponseWriter, r *http.Request) {
	p, err := a.validateAuthz(r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.renderLogin(w, p)
}

// tooManyFailures allows 5 bad passphrases per 15 minutes, server-wide.
func (a *AuthServer) tooManyFailures(record bool) bool {
	a.failMu.Lock()
	defer a.failMu.Unlock()
	cutoff := time.Now().Add(-15 * time.Minute)
	kept := a.failures[:0]
	for _, t := range a.failures {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	a.failures = kept
	if record {
		a.failures = append(a.failures, time.Now())
	}
	return len(a.failures) >= 5
}

func (a *AuthServer) authorizeSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	p, err := a.validateAuthz(r.PostForm)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if a.tooManyFailures(false) {
		p.Error = "Too many attempts. Try again in 15 minutes."
		a.renderLogin(w, p)
		return
	}
	a.mu.Lock()
	hash := a.st.PasswordHash
	a.mu.Unlock()
	if hash == "" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(r.PostForm.Get("password"))) != nil {
		a.tooManyFailures(true)
		slog.Warn("failed sign-in", "remote", clientIP(r))
		time.Sleep(time.Second)
		p.Error = "Wrong passphrase."
		if hash == "" {
			p.Error = "No passphrase set. Run: imessage-mcp set-password"
		}
		a.renderLogin(w, p)
		return
	}

	code := randToken()
	a.mu.Lock()
	for k, c := range a.codes {
		if time.Now().After(c.expires) {
			delete(a.codes, k)
		}
	}
	a.codes[hashToken(code)] = &authCode{
		clientID: p.ClientID, redirectURI: p.RedirectURI, challenge: p.Challenge,
		expires: time.Now().Add(codeTTL),
	}
	a.mu.Unlock()
	slog.Info("sign-in approved", "client", p.ClientName)

	u, _ := url.Parse(p.RedirectURI)
	q := u.Query()
	q.Set("code", code)
	if p.State != "" {
		q.Set("state", p.State)
	}
	q.Set("iss", a.issuer)
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (a *AuthServer) token(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "bad form")
		return
	}
	f := r.PostForm
	clientID := f.Get("client_id")

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.st.Clients[clientID] == nil {
		oauthError(w, http.StatusUnauthorized, "invalid_client", "unknown client")
		return
	}

	switch f.Get("grant_type") {
	case "authorization_code":
		key := hashToken(f.Get("code"))
		c := a.codes[key]
		delete(a.codes, key) // single use
		if c == nil || time.Now().After(c.expires) || c.clientID != clientID || c.redirectURI != f.Get("redirect_uri") {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "invalid or expired code")
			return
		}
		sum := sha256.Sum256([]byte(f.Get("code_verifier")))
		if subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(c.challenge)) != 1 {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
			return
		}
	case "refresh_token":
		key := hashToken(f.Get("refresh_token"))
		rec := a.st.Tokens[key]
		if rec == nil || rec.Kind != "refresh" || rec.ClientID != clientID || time.Now().After(rec.Expires) {
			oauthError(w, http.StatusBadRequest, "invalid_grant", "invalid refresh token")
			return
		}
		delete(a.st.Tokens, key) // rotate
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "")
		return
	}

	access, refresh := randToken(), randToken()
	now := time.Now()
	a.st.Tokens[hashToken(access)] = &tokenRec{Kind: "access", ClientID: clientID, Expires: now.Add(accessTTL)}
	a.st.Tokens[hashToken(refresh)] = &tokenRec{Kind: "refresh", ClientID: clientID, Expires: now.Add(refreshTTL)}
	if err := a.saveLocked(); err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "could not save token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    int(accessTTL.Seconds()),
		"refresh_token": refresh,
		"scope":         scope,
	})
}

// ---------------------------------------------------------------------------

func randToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(t string) string {
	s := sha256.Sum256([]byte(t))
	return hex.EncodeToString(s[:])
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func oauthError(w http.ResponseWriter, code int, kind, desc string) {
	writeJSON(w, code, map[string]string{"error": kind, "error_description": desc})
}

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("Cf-Connecting-Ip"); ip != "" {
		return ip
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}
