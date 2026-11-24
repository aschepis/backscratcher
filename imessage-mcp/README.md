# imessage-mcp

A read-only [MCP](https://modelcontextprotocol.io) server that lets Claude read
your iMessage/SMS history. It runs on your Mac, reads
`~/Library/Messages/chat.db` live, and is reached over a Cloudflare Tunnel.
No copy of your messages is stored anywhere else.

```
Claude ──https──▶ Cloudflare edge ──tunnel──▶ cloudflared (Mac) ──▶ imessage-mcp on 127.0.0.1:8765 ──read-only──▶ chat.db
```

## Tools

| Tool | What it does |
|---|---|
| `list_chats` | Conversations, most recent first, with participants and a preview. Filter by name, phone, email or group name. |
| `get_messages` | One conversation in order, by `chat_id` or `contact`. Page back with `before`. |
| `search_messages` | Messages containing every word of the query, newest first, optionally limited to a chat, contact or date range. |
| `recent_messages` | Everything from the last N hours across all chats ("what did I miss?"). |

Details:

- Reads the text Messages keeps only in `attributedBody` on macOS 13 and later. Without this, most recent messages look empty.
- Merges the iMessage and SMS threads you have with the same person.
- Resolves phone numbers and emails to names from your Contacts.
- Reports attachments by file name and type only. Their contents are never sent.
- Flags edited and unsent messages. Skips tapbacks unless you ask for them.

## Privacy controls (`config.json`)

The config file is created on first run at
`~/Library/Application Support/imessage-mcp/config.json`:

```json
{
  "listen": "127.0.0.1:8765",
  "public_url": "https://imessage.example.com",
  "max_age_days": 365,
  "exclude_chats": ["+15551234567", "Therapist", "Family Group"],
  "hide_short_codes": true,
  "redact_codes": true,
  "allowed_redirect_hosts": ["claude.ai", "claude.com", "localhost", "127.0.0.1"]
}
```

- `max_age_days`: nothing older than this is visible through any tool. Set it to `0` for no limit.
- `exclude_chats`: hides chats by phone number, email, group id, group name or contact name. Use exact matches; case is ignored.
- `hide_short_codes`: hides 3–6 digit senders (banks, 2FA, marketing).
- `redact_codes`: replaces the digits with `[redacted]` in messages that look like verification codes.
- `allowed_redirect_hosts`: only OAuth clients that redirect to these hosts can register.

Restart the server after you change the config.

## Security model

- **Read-only.** `chat.db` is opened with `mode=ro` and `query_only`. The server has no send tool.
- **Bound to loopback.** The server listens on `127.0.0.1` only. The internet reaches it through cloudflared, which handles TLS. A Host-header allowlist blocks DNS-rebinding attacks.
- **OAuth 2.1, single user.** Claude registers itself through dynamic client registration and sends you to a sign-in page. You approve with a passphrase only you know. Claude then gets a 24 h access token and a 60-day refresh token that rotates on every use. PKCE (S256) is required.
- **Brute-force limit.** After 5 wrong passphrases in 15 minutes, sign-in locks for everyone.
- **Hashed tokens.** Only SHA-256 hashes of tokens are stored, in `state.json` (mode 0600). The passphrase is stored as a bcrypt hash.
- **Quiet logs.** Logs record method, path and status only. They never contain message content, query strings or tokens.
- `imessage-mcp revoke-all` signs everything out right away. Running `set-password` again signs out every OAuth session.

What this does **not** protect against: the messages Claude reads become part of
that conversation. Cloudflare terminates TLS at its edge, so traffic between the
edge and your Mac is protected by the tunnel and not end to end. Message text is
written by other people and could contain prompt-injection attempts. The tools
are read-only, so the worst such a message can do is mislead Claude, not act
for you.

## Setup

### 1. Build and install (on the Mac)

```sh
cd imessage-mcp
go build -o ~/bin/imessage-mcp .
```

### 2. Grant Full Disk Access

`chat.db` is protected by macOS privacy controls (TCC). Open System Settings →
Privacy & Security → Full Disk Access → **+**, press ⌘⇧G, and add
`~/bin/imessage-mcp`. If you rebuild, macOS may ask again because the binary's
signature changed. Toggle the permission off and on, or remove and re-add the
binary.

Then check that it can read your messages:

```sh
~/bin/imessage-mcp doctor
```

If `doctor` fails from Terminal, give Terminal Full Disk Access as well, or run
it from launchd (step 5). launchd runs the binary directly, so the binary's own
grant is what counts there.

### 3. Set your passphrase

```sh
~/bin/imessage-mcp set-password
```

### 4. Create the Cloudflare Tunnel

You need a domain on Cloudflare.

```sh
brew install cloudflared
cloudflared tunnel login
cloudflared tunnel create imessage            # prints a tunnel ID
cloudflared tunnel route dns imessage imessage.example.com
```

Copy `deploy/cloudflared-config.yml` to `~/.cloudflared/config.yml`, then fill
in your hostname, user name and tunnel ID. Set `public_url` in `config.json` to
`https://imessage.example.com`.

Run the tunnel at login with launchd so it survives reboots:

```sh
sed "s/YOUR_USER/$USER/g" deploy/com.backscratcher.cloudflared.plist \
  > ~/Library/LaunchAgents/com.backscratcher.cloudflared.plist
launchctl load ~/Library/LaunchAgents/com.backscratcher.cloudflared.plist
tail -f ~/Library/Logs/cloudflared.log   # wait for "Registered tunnel connection"
```

`brew services start cloudflared` does not work here: it runs `cloudflared`
with no arguments, so it never starts the tunnel.

> Don't put Cloudflare Access in front of this hostname. Claude's servers need
> to reach the OAuth discovery and token endpoints without a browser login.
> The passphrase sign-in is the gate. You *can* add a Cloudflare WAF
> rate-limiting rule on `/authorize` and `/token` for extra protection.

### 5. Run at login with launchd

```sh
sed "s/YOUR_USER/$USER/g" deploy/com.backscratcher.imessage-mcp.plist \
  > ~/Library/LaunchAgents/com.backscratcher.imessage-mcp.plist
launchctl load ~/Library/LaunchAgents/com.backscratcher.imessage-mcp.plist
tail -f ~/Library/Logs/imessage-mcp.log
```

Check it from anywhere:
`curl https://imessage.example.com/.well-known/oauth-authorization-server`.

Keep the Mac from sleeping (System Settings → Battery / Energy → *Prevent
automatic sleeping when the display is off*). Otherwise the connector goes
offline when the Mac sleeps.

### 6. Connect Claude

**Claude (web, desktop or mobile):** Settings → Connectors → *Add custom
connector* → URL `https://imessage.example.com/mcp`. Click *Connect*, enter
your passphrase on the sign-in page, and you're done.

**Claude Code, from anywhere**, with a long-lived token:

```sh
TOKEN=$(~/bin/imessage-mcp token "claude code laptop")
claude mcp add --transport http imessage https://imessage.example.com/mcp \
  --header "Authorization: Bearer $TOKEN"
```

**Claude Desktop or Claude Code on the Mac itself** needs no tunnel. Use stdio:

```sh
claude mcp add imessage -- ~/bin/imessage-mcp stdio
```

## Development

```sh
go test ./...                       # uses a synthetic chat.db; runs on Linux too
GOOS=darwin GOARCH=arm64 go build .
```
