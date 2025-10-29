# frozen_string_literal: true

require "prompt_agent"
require "google/apis/gmail_v1"
require "googleauth"
require "json"
require "redcarpet"
require "loofah"
require "mail"

# Agent that summarizes unread emails based on a filter query
class EmailDigestAgent < PromptAgent
  PROMPT = <<~PROMPT
    Analyze the contents of the attached emails and produce a **detailed, deeply researched summary** that includes:
    1. **Thematic Organization:** Group insights by subject area (e.g., Immigration, Economy, Foreign Policy, Technology, Media, etc.).
    2. **Urgency & Deadlines:** Call out anything time-sensitive or urgent, including policy deadlines, legal actions,
       or crises with near-term consequences.
    3. **Fact-Checking:** Independently verify key claims using trusted public sources (news outlets, government reports, etc.).
       Note the source or confirm accuracy inline.
    4. **Correlations & Patterns:** Identify connections across stories or emails — e.g., how a budget cut affects multiple domains,
       or how legal rulings align with political messaging.
    5. **Signal Over Noise:** Summarize and clarify policy substance, not just rhetoric. Include context that helps explain *why*
       something matters.
    6. **Everything Counts:** Include all relevant email content — newsletters, alerts, op-eds, data points — without skipping
       opinionated or emotional commentary. Clearly distinguish opinion from fact, but preserve both.

    Present the final report as a cleanly formatted markdown summary with clear section headings and inline citations or references to
    the original emails when possible.

    Emails:
  PROMPT

  def initialize(filter_query:)
    @filter_query = filter_query
    super(prompt: PROMPT.gsub("{{filter_query}}", filter_query))
    @gmail_client = create_gmail_client
  end

  def process_data(data = nil)
    # This method is called by PromptAgent to process the fetched email data
    # and send it to the LLM along with the prompt
    data
  end

  def fetch_data
    # Fetch list of messages
    response =
      @gmail_client.list_user_messages(
        "me",
        label_ids: ["INBOX"],
        q: @filter_query
      )

    return [] unless response&.messages

    # Fetch full message details
    response
      .messages
      .map { |msg| @gmail_client.get_user_message("me", msg.id) }
      .map { |msg| extract_message_content(msg) }
      .join("\n\n")
  end

  def generate_output(output)
    profile = @gmail_client.get_user_profile("me")
    user_email = profile.email_address.to_s.strip.gsub(/[<>]/, "")

    subject_line = "Your Daily Gmail Digest #{Time.now.strftime("%Y-%m-%d")}"
    html_output = markdown_to_html(output)

    # Construct RFC 2822 message with all standard headers
    message_id = "<#{SecureRandom.hex(8)}@#{Socket.gethostname}>"
    date = Time.now.rfc2822

    raw_message = <<~EMAIL.gsub("\n", "\r\n")
      Date: #{date}
      From: #{user_email}
      To: #{user_email}
      Message-ID: #{message_id}
      Subject: #{subject_line}
      MIME-Version: 1.0
      Content-Type: text/html; charset=UTF-8

      #{html_output}
    EMAIL

    # Gmail API requires base64url encoding with no newlines
    encoded = Base64.urlsafe_encode64(raw_message).delete("\n")

    # Use REST API directly (google-api-client gem has issues with send_user_message)
    require "net/http"
    require "json"

    access_token = @gmail_client.authorization.access_token
    uri = URI("https://gmail.googleapis.com/gmail/v1/users/me/messages/send")

    http = Net::HTTP.new(uri.host, uri.port)
    http.use_ssl = true

    request = Net::HTTP::Post.new(uri.path)
    request["Authorization"] = "Bearer #{access_token}"
    request["Content-Type"] = "application/json"
    request.body = JSON.generate({ raw: encoded })

    response = http.request(request)

    raise "Gmail API error: #{response.body}" if response.code.to_i >= 400

    puts "✅ Email sent successfully! Message ID: #{JSON.parse(response.body)["id"]}"
    response
  end

  private

  def extract_message_content(message)
    headers = message.payload.headers

    from = headers.find { |h| h.name.downcase == "from" }&.value || "Unknown"
    subject =
      headers.find { |h| h.name.downcase == "subject" }&.value || "No Subject"

    # Create Gmail web link to message
    thread_id = message.thread_id
    gmail_link =
      (thread_id ? "https://mail.google.com/mail/u/0/#inbox/#{thread_id}" : nil)

    body_text = get_main_text_part(message.payload)

    "From: #{from}\nSubject: #{subject}\nLink: #{gmail_link}\n\n#{body_text}\n"
  end

  # Extract the body: prefer 'text/plain', fallback to 'text/html'
  def decode_body(body_data)
    return "" unless body_data

    Base64.urlsafe_decode64(body_data.to_s)
  rescue StandardError
    # it wasn't base64 encoded, so just return the string
    # avoid incompatible character encodings: UTF-8 and BINARY (ASCII-8BIT)
    body_data.to_s.force_encoding("UTF-8")
  end

  def get_main_text_part(payload)
    # Recursively collect all parts
    all_parts = collect_all_parts(payload)

    # Prefer text/plain, fallback to text/html
    text_part = all_parts.find { |part| part.mime_type == "text/plain" }
    return decode_body(text_part.body.data) if text_part

    html_part = all_parts.find { |part| part.mime_type == "text/html" }
    return decode_body(html_to_markdown(html_part.body.data)) if html_part

    # If payload itself is text, decode it directly
    if %w[text/plain text/html].include?(payload.mime_type)
      return decode_body(payload.body.data)
    end

    ""
  end

  def collect_all_parts(payload)
    # Base case: if no parts or empty parts, return the payload itself
    return [payload] unless payload.parts && !payload.parts.empty?

    # Recursive case: collect parts from all nested parts
    [payload] + payload.parts.flat_map { |p| collect_all_parts(p) }
  end

  def markdown_to_html(markdown)
    Redcarpet::Markdown.new(Redcarpet::Render::HTML).render(markdown)
  end

  def html_to_markdown(html)
    Loofah.fragment(html.to_s.force_encoding("UTF-8")).scrub!(:whitewash).to_s
  end

  def create_gmail_client
    %w[
      GMAIL_CLIENT_ID
      GMAIL_CLIENT_SECRET
      GMAIL_REFRESH_TOKEN
    ].each do |env_var|
      raise "#{env_var} is not set" unless ENV.fetch(env_var, nil)
    end

    # Specify scopes when using refresh token
    creds =
      Google::Auth::UserRefreshCredentials.new(
        client_id: ENV.fetch("GMAIL_CLIENT_ID", nil),
        client_secret: ENV.fetch("GMAIL_CLIENT_SECRET", nil),
        refresh_token: ENV.fetch("GMAIL_REFRESH_TOKEN", nil),
        scope: [
          Google::Apis::GmailV1::AUTH_GMAIL_SEND,
          Google::Apis::GmailV1::AUTH_GMAIL_MODIFY
        ]
      )

    gmail = Google::Apis::GmailV1::GmailService.new
    gmail.authorization = creds
    gmail
  end
end
