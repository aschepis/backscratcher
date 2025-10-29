# frozen_string_literal: true

require "googleauth"
require "googleauth/stores/file_token_store"
require "google/apis/gmail_v1"
require "webrick"
require "fileutils"

namespace :gmail do
  desc "Authenticate with Google and store Gmail OAuth refresh token"
  task :oauth do
    client_id = ENV.fetch("GMAIL_CLIENT_ID")
    client_secret = ENV.fetch("GMAIL_CLIENT_SECRET")

    # Use a local YAML file to store the refresh token
    token_path = ".gmail_oauth_token.yaml"
    FileUtils.touch(token_path)
    token_store = Google::Auth::Stores::FileTokenStore.new(file: token_path)

    scope = [
      Google::Apis::GmailV1::AUTH_GMAIL_SEND,
      Google::Apis::GmailV1::AUTH_GMAIL_MODIFY
    ]

    redirect_uri = "http://localhost:8888/oauth2callback"
    user_id = "me"

    client = Google::Auth::ClientId.new(client_id, client_secret)
    authorizer = Google::Auth::UserAuthorizer.new(client, scope, token_store)

    # ✅ Always request a new consent so we get a proper refresh token
    url =
      authorizer.get_authorization_url(
        base_url: redirect_uri,
        prompt: "consent",
        access_type: "offline"
      )

    puts "🔗 Visit this URL to authorize Gmail access:"
    puts url

    server =
      WEBrick::HTTPServer.new(
        Port: 8888,
        AccessLog: [],
        Logger: WEBrick::Log.new(nil, WEBrick::Log::ERROR)
      )

    server.mount_proc "/oauth2callback" do |req, res|
      if req.query["code"]
        authorizer.get_and_store_credentials_from_code(
          user_id: user_id,
          code: req.query["code"],
          base_url: redirect_uri
        )
        res.body =
          "<h1>✅ Authorization complete. You can close this window.</h1>"
        server.shutdown
      else
        res.body = "Missing authorization code!"
      end
    end

    trap("INT") { server.shutdown }
    server.start

    puts "\n🚦 Waiting for authorization..."

    # This waits for your local server to complete the handshake and write token
    credentials = nil
    until credentials
      sleep 1
      credentials = authorizer.get_credentials(user_id)
    end

    puts "✅ OAuth success!"

    # 🔍 Validate that we authenticated as a real Gmail user
    gmail = Google::Apis::GmailV1::GmailService.new
    gmail.authorization = credentials

    begin
      profile = gmail.get_user_profile(user_id)
      labels = gmail.list_user_labels(user_id)

      puts "-----------------------------------"
      puts "Authenticated Gmail identity:"
      puts "📧 #{profile.email_address}"
      puts "✅ Gmail mailbox detected" if labels.labels.any?
      puts "📍 Token saved to #{token_path}"
      puts "-----------------------------------"
    rescue StandardError => e
      puts "⚠️ Gmail mailbox could not be accessed"
      puts e.message
    end

    # 🎁 Show user where refresh token lives now
    puts "\nRefresh Token:"
    puts credentials.refresh_token
    puts "\n→ Save this in ENV[GMAIL_REFRESH_TOKEN]\n"
  end
end
