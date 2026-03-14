# frozen_string_literal: true

require "prompt_agent"
require "google/apis/gmail_v1"
require "googleauth"
require "json"
require "redcarpet"
require "loofah"
require "mail"

# Agent that fetches unread emails from the "digestme" label and generates
# a separate digest for each topic of interest via a single Claude request per topic.
class EmailDigestAgent < PromptAgent
  MAX_INPUT_TOKENS      = 1_000_000
  INPUT_TOKEN_SAFETY_MARGIN = 10_000
  MAX_EMAIL_AGE_HOURS   = 48
  DIGEST_LABEL          = "digestme"
  LONG_CONTEXT_BETA     = "context-1m-2025-08-07"

  TOPICS = %w[Education SocialJustice AITech DevTools General Politics].freeze

  PROMPT_TEMPLATE = <<~PROMPT.freeze
    You are generating a digest for emails on the topic: **%<topic>s**.

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

  def initialize
    super(prompt: "")
    @gmail_client = create_gmail_client
  end

  def run
    @logger.info("Running agent #{name}")

    email_data = fetch_emails
    if email_data.empty?
      @logger.info("No unread messages in #{DIGEST_LABEL}, skipping")
      return
    end

    TOPICS.each do |topic|
      @logger.info("Generating digest for topic: #{topic}")
      prompt = format(PROMPT_TEMPLATE, topic: topic)
      summary = stream_request(prompt, email_data)
      send_digest_email(summary, topic)
    end
  end

  private

  # Returns all unread emails from the digestme label as a single string.
  def fetch_emails
    label_id = find_label_id(DIGEST_LABEL)
    unless label_id
      @logger.warn("Label '#{DIGEST_LABEL}' not found in Gmail")
      return ""
    end

    newer_than_days = (MAX_EMAIL_AGE_HOURS / 24.0).ceil
    response = @gmail_client.list_user_messages(
      "me",
      label_ids: ["INBOX", label_id],
      q: "is:unread newer_than:#{newer_than_days}d"
    )
    return "" unless response&.messages

    cutoff = (Time.now - (MAX_EMAIL_AGE_HOURS * 3600)).to_i
    max_data_tokens = MAX_INPUT_TOKENS - INPUT_TOKEN_SAFETY_MARGIN

    chunks = []
    used_tokens = 0

    response
      .messages
      .map { |msg| @gmail_client.get_user_message("me", msg.id) }
      .select { |msg| msg.internal_date.to_i >= cutoff }
      .sort_by { |msg| -msg.internal_date.to_i }
      .each do |msg|
        chunk = extract_message_content(msg)
        chunk_tokens = approx_input_tokens(chunk)
        break if used_tokens + chunk_tokens > max_data_tokens

        chunks << chunk
        used_tokens += chunk_tokens
      end

    @logger.info("Loaded #{chunks.size} emails (~#{used_tokens} tokens)")
    chunks.join("\n\n")
  end

  def stream_request(prompt, data)
    content = "#{prompt}\n\n#{data}"
    stream = @anthropic_client.beta.messages.stream(
      model: "claude-sonnet-4-6",
      max_tokens: 32_000,
      messages: [{ role: "user", content: content }],
      betas: [LONG_CONTEXT_BETA]
    )

    result = +""
    stream.text.each { |text| result << text }
    result
  end

  def find_label_id(name)
    labels_response = @gmail_client.list_user_labels("me")
    return nil unless labels_response&.labels

    label = labels_response.labels.find { |l| l.name.downcase == name.downcase }
    label&.id
  end

  def send_digest_email(output, topic)
    profile = @gmail_client.get_user_profile("me")
    user_email = profile.email_address.to_s.strip.gsub(/[<>]/, "")

    topic_suffix = topic == "General" ? "" : ": #{topic}"
    subject_line = "Your Daily Gmail Digest#{topic_suffix} #{Time.now.strftime('%Y-%m-%d')}"
    html_output = markdown_to_html(output)

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

    encoded = Base64.urlsafe_encode64(raw_message).delete("\n")

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

    puts "Email sent for #{topic} digest! Message ID: #{JSON.parse(response.body)['id']}"
    response
  end

  def extract_message_content(message)
    headers = message.payload.headers
    from    = headers.find { |h| h.name.downcase == "from" }&.value || "Unknown"
    subject = headers.find { |h| h.name.downcase == "subject" }&.value || "No Subject"
    link    = message.thread_id ? "https://mail.google.com/mail/u/0/#inbox/#{message.thread_id}" : nil
    body    = get_main_text_part(message.payload)

    "From: #{from}\nSubject: #{subject}\nLink: #{link}\n\n#{body}\n"
  end

  def decode_body(body_data)
    return "" unless body_data

    Base64.urlsafe_decode64(body_data.to_s)
  rescue StandardError
    body_data.to_s.force_encoding("UTF-8")
  end

  def get_main_text_part(payload)
    all_parts = collect_all_parts(payload)

    text_part = all_parts.find { |part| part.mime_type == "text/plain" }
    return decode_body(text_part.body.data) if text_part

    html_part = all_parts.find { |part| part.mime_type == "text/html" }
    return decode_body(html_to_markdown(html_part.body.data)) if html_part

    return decode_body(payload.body.data) if %w[text/plain text/html].include?(payload.mime_type)

    ""
  end

  def collect_all_parts(payload)
    return [payload] unless payload.parts && !payload.parts.empty?

    [payload] + payload.parts.flat_map { |p| collect_all_parts(p) }
  end

  def markdown_to_html(markdown)
    Redcarpet::Markdown.new(Redcarpet::Render::HTML).render(markdown)
  end

  def html_to_markdown(html)
    Loofah.fragment(html.to_s.force_encoding("UTF-8")).scrub!(:whitewash).to_s
  end

  def create_gmail_client
    %w[GMAIL_CLIENT_ID GMAIL_CLIENT_SECRET GMAIL_REFRESH_TOKEN].each do |var|
      raise "#{var} is not set" unless ENV.fetch(var, nil)
    end

    creds = Google::Auth::UserRefreshCredentials.new(
      client_id:     ENV.fetch("GMAIL_CLIENT_ID"),
      client_secret: ENV.fetch("GMAIL_CLIENT_SECRET"),
      refresh_token: ENV.fetch("GMAIL_REFRESH_TOKEN"),
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
