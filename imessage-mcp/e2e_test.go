package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearer struct {
	tok  string
	base http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.tok)
	return b.base.RoundTrip(r)
}

func TestOAuthAndMCPEndToEnd(t *testing.T) {
	store := fixture(t)
	var ts *httptest.Server
	ts = httptest.NewUnstartedServer(nil)
	issuer := "http://" + ts.Listener.Addr().String()
	cfg := store.cfg
	a, err := loadAuth(t.TempDir(), issuer, cfg.AllowedRedirectHosts)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SetPassword("correct horse battery"); err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = buildHandler(cfg, a, store)
	ts.Start()
	defer ts.Close()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// 1. Unauthenticated MCP request is challenged with the metadata URL.
	resp, _ := http.Post(ts.URL+"/mcp", "application/json", strings.NewReader("{}"))
	if resp.StatusCode != 401 || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "resource_metadata=") {
		t.Fatalf("unauth: %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}

	// Wrong Host header is refused (DNS rebinding).
	req, _ := http.NewRequest("GET", ts.URL+"/.well-known/oauth-authorization-server", nil)
	req.Host = "evil.example"
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("bad host: %d", resp.StatusCode)
	}

	// 2. Metadata.
	var meta map[string]any
	resp, _ = http.Get(ts.URL + "/.well-known/oauth-authorization-server")
	json.NewDecoder(resp.Body).Decode(&meta)
	if meta["token_endpoint"] != issuer+"/token" {
		t.Fatalf("meta: %v", meta)
	}

	// 3. Dynamic client registration; foreign redirect hosts are refused.
	resp, _ = http.Post(ts.URL+"/register", "application/json", strings.NewReader(`{"client_name":"x","redirect_uris":["https://evil.example/cb"]}`))
	if resp.StatusCode != 400 {
		t.Fatalf("evil redirect accepted: %d", resp.StatusCode)
	}
	const redirect = "https://claude.ai/api/mcp/auth_callback"
	resp, _ = http.Post(ts.URL+"/register", "application/json", strings.NewReader(`{"client_name":"Claude","redirect_uris":["`+redirect+`"]}`))
	var reg map[string]any
	json.NewDecoder(resp.Body).Decode(&reg)
	clientID, _ := reg["client_id"].(string)
	if clientID == "" {
		t.Fatalf("register: %v", reg)
	}

	// 4. Authorize.
	verifier := "verifier-verifier-verifier-verifier-verifier-1234"
	sum := sha256.Sum256([]byte(verifier))
	form := url.Values{
		"client_id": {clientID}, "redirect_uri": {redirect}, "state": {"st"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
		"response_type": {"code"}, "password": {"wrong password!!"},
	}
	resp, _ = http.Get(ts.URL + "/authorize?" + form.Encode())
	if body, _ := io.ReadAll(resp.Body); resp.StatusCode != 200 || !strings.Contains(string(body), "Claude") {
		t.Fatalf("authorize form: %d", resp.StatusCode)
	}
	resp, _ = noRedirect.PostForm(ts.URL+"/authorize", form)
	if resp.StatusCode != 200 {
		t.Fatalf("wrong password should re-render, got %d", resp.StatusCode)
	}
	form.Set("password", "correct horse battery")
	resp, _ = noRedirect.PostForm(ts.URL+"/authorize", form)
	loc, _ := url.Parse(resp.Header.Get("Location"))
	code := loc.Query().Get("code")
	if resp.StatusCode != 302 || code == "" || loc.Query().Get("state") != "st" || !strings.HasPrefix(loc.String(), redirect) {
		t.Fatalf("authorize: %d %s", resp.StatusCode, loc)
	}

	// 5. Token exchange: bad verifier fails, and codes are single-use.
	tokenReq := func(v url.Values) map[string]any {
		resp, _ := http.PostForm(ts.URL+"/token", v)
		var m map[string]any
		json.NewDecoder(resp.Body).Decode(&m)
		return m
	}
	bad := tokenReq(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID}, "redirect_uri": {redirect}, "code_verifier": {"nope"}})
	if bad["error"] != "invalid_grant" {
		t.Fatalf("bad verifier: %v", bad)
	}
	resp, _ = noRedirect.PostForm(ts.URL+"/authorize", form)
	loc, _ = url.Parse(resp.Header.Get("Location"))
	code = loc.Query().Get("code")
	tok := tokenReq(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID}, "redirect_uri": {redirect}, "code_verifier": {verifier}})
	access, _ := tok["access_token"].(string)
	refresh, _ := tok["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatalf("token: %v", tok)
	}
	if again := tokenReq(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID}, "redirect_uri": {redirect}, "code_verifier": {verifier}}); again["error"] == nil {
		t.Fatal("code reused")
	}

	// Refresh tokens rotate.
	r1 := tokenReq(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}})
	if r1["access_token"] == nil {
		t.Fatalf("refresh: %v", r1)
	}
	if r2 := tokenReq(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}}); r2["error"] == nil {
		t.Fatal("refresh token reused")
	}

	// 6. Real MCP session with the access token.
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   ts.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearer{access, http.DefaultTransport}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	tools, err := sess.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 4 {
		t.Fatalf("tools: %v %v", tools, err)
	}
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "search_messages", Arguments: map[string]any{"query": "PIZZA"}})
	if err != nil || res.IsError {
		t.Fatalf("call: %v %+v", err, res)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "Sure, pizza at 7") {
		t.Fatalf("search result: %s", text)
	}
	res, _ = sess.CallTool(ctx, &mcp.CallToolParams{Name: "recent_messages", Arguments: map[string]any{"hours": 3, "only_incoming": true}})
	text = res.Content[0].(*mcp.TextContent).Text
	if strings.Contains(text, `"me"`) || !strings.Contains(text, "check this out") {
		t.Fatalf("recent: %s", text)
	}

	// Static tokens work too, and revoke-all kills everything.
	static, _ := a.NewStaticToken("test")
	if _, err := a.Verify(ctx, static, nil); err != nil {
		t.Fatal(err)
	}
	a.RevokeAll()
	if _, err := a.Verify(ctx, access, nil); err == nil {
		t.Fatal("token survived revoke-all")
	}
}
