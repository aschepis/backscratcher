# frozen_string_literal: true

require "prompt_agent"
require "google/apis/gmail_v1"
require "googleauth"
require "json"

# Agent that analyzes inbox history and suggests Gmail filter rules
# for emails suitable for digest automation. Read-only — no labels are applied.
class EmailFilterAgent < PromptAgent
  MAX_INPUT_TOKENS = 200_000
  INPUT_TOKEN_SAFETY_MARGIN = 10_000

  # Headers we extract from each message
  EXTRACTED_HEADERS = %w[From Subject List-Id List-Unsubscribe Precedence Reply-To].freeze

  # Max sample subjects per sender group
  MAX_SAMPLE_SUBJECTS = 5

  PROMPT = <<~PROMPT
    You are an expert email management consultant. Analyze the following summary of email senders
    from the user's inbox and suggest Gmail filter rules for emails that should be automatically
    labeled for a daily digest.

    ## Definitions

    **Digestable emails** — newsletters, mailing lists, automated notifications, marketing emails,
    service alerts, social media digests, shopping/order updates, developer tool notifications.
    These are emails the user likely wants to see but doesn't need to read immediately.

    **Personal emails** — direct human-to-human conversation. NEVER suggest these for digest.
    If a sender looks like a real person writing directly (no list headers, low volume, personal
    subject lines), classify as personal and exclude.

    ## Your Task

    For each sender in the data below:
    1. Classify as DIGEST or PERSONAL (with brief reasoning for borderline cases)
    2. For DIGEST senders, assign a category (e.g., Politics, Tech, Finance, News, Social,
       Shopping, Dev Tools, Marketing, Travel, Health, Entertainment, etc.)
    3. Rate signal-to-noise: HIGH (most emails are useful), MEDIUM (hit or miss), LOW (mostly noise)

    ## Output Format

    First, output the categorized filter rules. Only include HIGH and MEDIUM signal senders.
    Group by category and format as Gmail filter rules:

    ### Category: [Category Name]
    Suggested label: `Digest/[CategoryName]`
    ```
    from:(sender1@example.com OR sender2@example.com OR sender3@example.com)
    ```
    Senders:
    - sender1@example.com — [Brief description] [HIGH/MEDIUM]
    - sender2@example.com — [Brief description] [HIGH/MEDIUM]

    Then at the end, provide:

    ### Summary
    - Total senders analyzed: N
    - Digest candidates: N (HIGH: N, MEDIUM: N)
    - Personal/excluded: N
    - Categories: list of categories with counts

    ### Excluded Personal Senders
    Brief list of senders classified as personal (so the user can verify).

    ## Sender Data

  PROMPT

  def initialize(days:)
    @days = days
    super(prompt: PROMPT)
    @gmail_client = create_gmail_client
  end

  def fetch_data
    @logger.info("Fetching email metadata for the last #{@days} days")

    messages = fetch_all_message_metadata
    @logger.info("Fetched metadata for #{messages.size} messages")

    return "No messages found in the last #{@days} days." if messages.empty?

    groups = group_by_sender(messages)
    @logger.info("Found #{groups.size} unique senders")

    format_sender_groups(groups)
  end

  def generate_output(analysis)
    puts "=" * 80
    puts "EMAIL FILTER SUGGESTIONS"
    puts "Analyzed #{@days} days of inbox history"
    puts "Generated #{Time.now.strftime('%Y-%m-%d %H:%M')}"
    puts "=" * 80
    puts
    puts analysis
    puts
    puts "=" * 80
    puts "END OF REPORT"
    puts "=" * 80
  end

  private

  def fetch_all_message_metadata
    query = "newer_than:#{@days}d"
    all_messages = []
    page_token = nil

    loop do
      response = @gmail_client.list_user_messages(
        "me",
        label_ids: ["INBOX"],
        q: query,
        page_token: page_token,
        max_results: 500
      )

      break unless response&.messages

      response.messages.each do |msg_ref|
        msg = @gmail_client.get_user_message("me", msg_ref.id, format: "metadata",
                                                                metadata_headers: EXTRACTED_HEADERS)
        all_messages << msg
      end

      page_token = response.next_page_token
      break unless page_token

      @logger.info("Fetched #{all_messages.size} messages so far, continuing pagination...")
    end

    all_messages
  end

  def group_by_sender(messages)
    groups = Hash.new { |h, k| h[k] = { subjects: [], count: 0, headers: {} } }

    messages.each do |msg|
      headers = extract_headers(msg)
      sender_email = normalize_sender(headers["from"])
      next unless sender_email

      group = groups[sender_email]
      group[:count] += 1
      group[:subjects] << headers["subject"] if headers["subject"]

      # Merge header presence flags (keep track if any message had these)
      %w[list-id list-unsubscribe precedence reply-to].each do |key|
        group[:headers][key] = true if headers[key]
      end

      # Store the raw From for display (first one seen)
      group[:from_display] ||= headers["from"]
    end

    groups
  end

  def extract_headers(message)
    result = {}
    return result unless message.payload&.headers

    message.payload.headers.each do |header|
      key = header.name.downcase
      result[key] = header.value if EXTRACTED_HEADERS.any? { |h| h.downcase == key }
    end

    result
  end

  def normalize_sender(from)
    return nil unless from

    # Extract bare email from "Display Name <email@example.com>" or plain "email@example.com"
    match = from.match(/<([^>]+)>/)
    email = match ? match[1] : from
    email.strip.downcase
  end

  def format_sender_groups(groups)
    prompt_tokens = approx_input_tokens(PROMPT)
    max_data_tokens = MAX_INPUT_TOKENS - INPUT_TOKEN_SAFETY_MARGIN - prompt_tokens

    # Sort by message count descending so we keep high-volume senders if we need to truncate
    sorted = groups.sort_by { |_email, data| -data[:count] }

    lines = []
    used_tokens = 0

    sorted.each do |email, data|
      entry = format_sender_entry(email, data)
      entry_tokens = approx_input_tokens(entry)

      break if used_tokens + entry_tokens > max_data_tokens

      lines << entry
      used_tokens += entry_tokens
    end

    if lines.size < sorted.size
      @logger.info("Token budget reached: included #{lines.size}/#{sorted.size} senders")
    end

    lines.join("\n")
  end

  def format_sender_entry(email, data)
    sample_subjects = data[:subjects].uniq.first(MAX_SAMPLE_SUBJECTS).map { |s| "  - #{s}" }

    header_flags = []
    header_flags << "list-id" if data[:headers]["list-id"]
    header_flags << "list-unsubscribe" if data[:headers]["list-unsubscribe"]
    header_flags << "precedence" if data[:headers]["precedence"]

    parts = []
    parts << "Sender: #{email}"
    parts << "Display: #{data[:from_display]}" if data[:from_display] && data[:from_display] != email
    parts << "Count: #{data[:count]}"
    parts << "List headers: #{header_flags.join(', ')}" unless header_flags.empty?
    parts << "Sample subjects:" unless sample_subjects.empty?
    parts.concat(sample_subjects)
    parts << ""

    parts.join("\n")
  end

  def create_gmail_client
    %w[
      GMAIL_CLIENT_ID
      GMAIL_CLIENT_SECRET
      GMAIL_REFRESH_TOKEN
    ].each do |env_var|
      raise "#{env_var} is not set" unless ENV.fetch(env_var, nil)
    end

    creds = Google::Auth::UserRefreshCredentials.new(
      client_id: ENV.fetch("GMAIL_CLIENT_ID", nil),
      client_secret: ENV.fetch("GMAIL_CLIENT_SECRET", nil),
      refresh_token: ENV.fetch("GMAIL_REFRESH_TOKEN", nil),
      scope: [Google::Apis::GmailV1::AUTH_GMAIL_READONLY]
    )

    gmail = Google::Apis::GmailV1::GmailService.new
    gmail.authorization = creds
    gmail
  end
end
