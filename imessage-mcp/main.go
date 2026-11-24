// Command imessage-mcp serves your Mac's iMessage history to Claude over MCP.
//
// It reads ~/Library/Messages/chat.db live and read-only. Run it on the Mac,
// expose it with a Cloudflare Tunnel, and add it to Claude as a custom
// connector. See README.md.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/term"
)

const version = "0.1.0"

const usage = `imessage-mcp: read-only iMessage MCP server

Usage:
  imessage-mcp serve          run the HTTP MCP server (default)
  imessage-mcp stdio          run over stdio (Claude Desktop / Claude Code on this Mac)
  imessage-mcp set-password   set the passphrase used to approve OAuth sign-ins
  imessage-mcp token [label]  print a new long-lived bearer token
  imessage-mcp revoke-all     sign out every client and delete every token
  imessage-mcp doctor         check database access and print a summary

Config: %s/config.json
`

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	dir, err := configDir()
	if err != nil {
		fatal(err)
	}
	flag.Usage = func() { fmt.Fprintf(os.Stderr, usage, dir) }
	flag.Parse()

	cmd := "serve"
	if flag.NArg() > 0 {
		cmd = flag.Arg(0)
	}
	cfgPath, err := writeConfigIfMissing(dir)
	if err != nil {
		fatal(err)
	}
	cfg, err := loadConfig(dir)
	if err != nil {
		fatal(err)
	}

	switch cmd {
	case "serve":
		serve(dir, cfg)
	case "stdio":
		runStdio(cfg)
	case "set-password":
		setPassword(dir, cfg)
	case "token":
		a := mustAuth(dir, cfg)
		label := strings.Join(flag.Args()[1:], " ")
		tok, err := a.NewStaticToken(label)
		if err != nil {
			fatal(err)
		}
		fmt.Println(tok)
		fmt.Fprintln(os.Stderr, "Store this somewhere safe; it is not shown again. Revoke with: imessage-mcp revoke-all")
	case "revoke-all":
		if err := mustAuth(dir, cfg).RevokeAll(); err != nil {
			fatal(err)
		}
		fmt.Println("All clients and tokens revoked.")
	case "doctor":
		doctor(cfg, cfgPath)
	case "help", "-h", "--help":
		flag.Usage()
	default:
		flag.Usage()
		os.Exit(2)
	}
}

func mustAuth(dir string, cfg Config) *AuthServer {
	a, err := loadAuth(dir, issuerFor(cfg), cfg.AllowedRedirectHosts)
	if err != nil {
		fatal(err)
	}
	return a
}

func issuerFor(cfg Config) string {
	if cfg.PublicURL != "" {
		return strings.TrimRight(cfg.PublicURL, "/")
	}
	return "http://" + cfg.Listen
}

func mustStore(cfg Config) *Store {
	contacts := newContacts()
	contacts.refreshEvery(10 * time.Minute)
	store, err := openStore(cfg, contacts)
	if err != nil {
		fatal(err)
	}
	return store
}

func serve(dir string, cfg Config) {
	a := mustAuth(dir, cfg)
	if !a.HasPassword() {
		slog.Warn("no passphrase set: OAuth sign-in will fail until you run `imessage-mcp set-password`")
	}
	if cfg.PublicURL == "" {
		slog.Warn("public_url not set in config.json: OAuth only works for local clients")
	} else if !strings.HasPrefix(cfg.PublicURL, "https://") {
		fatal(fmt.Errorf("public_url must be https:// (got %q)", cfg.PublicURL))
	}
	store := mustStore(cfg)
	defer store.Close()

	hs := &http.Server{
		Addr:              cfg.Listen,
		Handler:           buildHandler(cfg, a, store),
		ReadHeaderTimeout: 10 * time.Second,
	}
	if host, _, err := net.SplitHostPort(cfg.Listen); err == nil {
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			slog.Warn("listening on a non-loopback address; anyone on your network can reach the OAuth endpoints", "listen", cfg.Listen)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hs.Shutdown(shutdown)
	}()
	slog.Info("serving", "listen", cfg.Listen, "mcp", issuerFor(cfg)+"/mcp", "contacts", store.contacts.Len())
	if err := hs.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fatal(err)
	}
}

func buildHandler(cfg Config, a *AuthServer, store *Store) http.Handler {
	srv := newMCPServer(store)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		// The SDK rejects non-localhost Host headers on loopback connections
		// (DNS-rebinding protection). cloudflared connects from loopback with
		// the public Host, so we disable it and enforce our own Host allowlist
		// in hostGuard instead.
		DisableLocalhostProtection: true,
		SessionTimeout:             30 * time.Minute,
	})
	protected := auth.RequireBearerToken(a.Verify, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: a.ResourceMetadataURL(),
		Scopes:              []string{scope},
	})(mcpHandler)

	mux := http.NewServeMux()
	a.Register(mux)
	mux.Handle("/mcp", protected)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "imessage-mcp is running. MCP endpoint: /mcp")
	})

	return logRequests(hostGuard(cfg, mux))
}

// hostGuard only answers requests addressed to the configured public host or
// to localhost, which blocks DNS-rebinding attacks from web pages.
func hostGuard(cfg Config, next http.Handler) http.Handler {
	allowed := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	if u, err := url.Parse(cfg.PublicURL); err == nil && u.Hostname() != "" {
		allowed[strings.ToLower(u.Hostname())] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(strings.ToLower(host), "[]")
		if !allowed[host] {
			http.Error(w, "unknown host", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (s *statusWriter) WriteHeader(c int) { s.code = c; s.ResponseWriter.WriteHeader(c) }
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// logRequests logs method, path and status only: never bodies or query
// strings, so message content and OAuth codes stay out of the logs.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: 200}
		next.ServeHTTP(sw, r)
		slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.code,
			"ms", time.Since(start).Milliseconds(), "remote", clientIP(r))
	})
}

func runStdio(cfg Config) {
	store := mustStore(cfg)
	defer store.Close()
	if err := newMCPServer(store).Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fatal(err)
	}
}

func setPassword(dir string, cfg Config) {
	a := mustAuth(dir, cfg)
	read := func(prompt string) string {
		fmt.Fprint(os.Stderr, prompt)
		if term.IsTerminal(int(os.Stdin.Fd())) {
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if err != nil {
				fatal(err)
			}
			return string(b)
		}
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		return strings.TrimRight(line, "\r\n")
	}
	pw := read("New passphrase (12+ characters): ")
	if len(pw) < 12 {
		fatal(fmt.Errorf("passphrase must be at least 12 characters"))
	}
	if term.IsTerminal(int(os.Stdin.Fd())) && read("Again: ") != pw {
		fatal(fmt.Errorf("passphrases did not match"))
	}
	if err := a.SetPassword(pw); err != nil {
		fatal(err)
	}
	fmt.Println("Passphrase saved. Existing OAuth sessions were signed out.")
}

func doctor(cfg Config, cfgPath string) {
	fmt.Println("config:", cfgPath)
	path, _ := cfg.chatDBPath()
	fmt.Println("chat.db:", path)
	contacts := newContacts()
	fmt.Printf("contacts: %d phone numbers/emails\n", contacts.Len())
	store, err := openStore(cfg, contacts)
	if err != nil {
		fmt.Println("ERROR:", err)
		fmt.Println("\nGrant Full Disk Access to this binary (or the terminal running it):")
		fmt.Println("  System Settings → Privacy & Security → Full Disk Access → +")
		os.Exit(1)
	}
	defer store.Close()
	ctx := context.Background()
	chats, err := store.ListChats(ctx, "", time.Time{}, 5)
	if err != nil {
		fatal(err)
	}
	ts, _ := store.threads(ctx)
	fmt.Printf("visible chats: %d (max_age_days=%d)\n", len(uniqueThreads(ts)), cfg.MaxAgeDays)
	fmt.Println("most recent:")
	for _, c := range chats {
		fmt.Printf("  [%d] %s  (%s)\n", c.ChatID, c.Name, c.LastMessage)
	}
	fmt.Println("public_url:", orNone(cfg.PublicURL))
}

func orNone(s string) string {
	if s == "" {
		return "(not set)"
	}
	return s
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "imessage-mcp:", err)
	os.Exit(1)
}
