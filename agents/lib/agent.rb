# frozen_string_literal: true

require "base_agent"
require "anthropic"
require "yaml"
require "google/apis/gmail_v1"
require "googleauth"
require "json"
require "redcarpet"
require "loofah"
require "mail"
require "base64"
require "net/http"

# Configuration-driven Agent implementation
# Agents are defined by their configuration file which specifies:
# - name: Human-readable name
# - id: Unique identifier
# - personality: System instructions/personality
# - assignment: What this agent does
# - capabilities: What the agent can do
# - tools: Available tools (gmail, etc.)
# - prompt_template: Template for the agent's prompt
# - output_handler: How to handle output (email, stdout, etc.)
class Agent < BaseAgent
  attr_reader :config, :id, :name, :personality, :assignment, :capabilities, :tools

  def initialize(config_path: nil, config_hash: nil, config_overrides: {})
    super()
    
    if config_path
      @config = load_config_from_file(config_path)
    elsif config_hash
      @config = config_hash
    else
      raise ArgumentError, "Either config_path or config_hash must be provided"
    end

    # Apply overrides (deep merge)
    @config = deep_merge(@config, config_overrides) unless config_overrides.empty?

    validate_config!
    initialize_from_config
    
    @anthropic_client = Anthropic::Client.new(api_key: ENV.fetch("ANTHROPIC_API_KEY", nil)) if needs_llm?
    @gmail_client = create_gmail_client if has_tool?("gmail")
  end

  def needs_llm?
    @config["prompt_template"] || @config["personality"]
  end

  def has_tool?(tool_name)
    (@config["tools"] || []).include?(tool_name)
  end

  def fetch_data
    @logger.info("Fetching data for agent #{@name}")
    
    return nil unless has_tool?("gmail")
    
    filter_query = @config.dig("gmail", "filter_query") || 
                   ENV.fetch("EMAIL_DIGEST_FILTER_QUERY", "in:inbox is:unread")
    
    response = @gmail_client.list_user_messages(
      "me",
      label_ids: ["INBOX"],
      q: filter_query
    )

    return "" unless response&.messages

    response
      .messages
      .map { |msg| @gmail_client.get_user_message("me", msg.id) }
      .map { |msg| extract_message_content(msg) }
      .join("\n\n")
  end

  def process_data(data = nil)
    return data unless needs_llm?

    prompt = build_prompt(data)
    
    @logger.info("Processing data with LLM for agent #{@name}")
    
    stream = @anthropic_client.messages.stream(
      model: @config.dig("llm", "model") || "claude-sonnet-4-5-20250929",
      max_tokens: @config.dig("llm", "max_tokens") || 32_000,
      messages: [
        { role: "system", content: build_system_prompt },
        { role: "user", content: prompt }
      ]
    )

    result = +""
    stream.text.each { |text| result << text }
    result
  end

  def generate_output(output)
    @logger.info("Generating output for agent #{@name}")
    
    handler = @config["output_handler"] || "stdout"
    
    case handler
    when "email"
      send_email_output(output)
    when "stdout"
      puts output
    when "skip_if_no_action"
      return if output.include?("<no action required>")
      send_email_output(output)
    else
      puts output
    end
    
    output
  end

  private

  def deep_merge(hash1, hash2)
    hash1.merge(hash2) do |_key, old_val, new_val|
      if old_val.is_a?(Hash) && new_val.is_a?(Hash)
        deep_merge(old_val, new_val)
      else
        new_val
      end
    end
  end

  def load_config_from_file(path)
    YAML.load_file(path)
  rescue Errno::ENOENT
    raise "Configuration file not found: #{path}"
  rescue Psych::SyntaxError => e
    raise "Invalid YAML in configuration file: #{e.message}"
  end

  def validate_config!
    required_fields = ["id", "name"]
    missing = required_fields.reject { |field| @config.key?(field) }
    
    raise "Missing required configuration fields: #{missing.join(", ")}" unless missing.empty?
  end

  def initialize_from_config
    @id = @config["id"]
    @name = @config["name"]
    @personality = @config["personality"] || ""
    @assignment = @config["assignment"] || ""
    @capabilities = @config["capabilities"] || []
    @tools = @config["tools"] || []
  end

  def build_system_prompt
    parts = []
    parts << @personality if @personality && !@personality.empty?
    parts << "Assignment: #{@assignment}" if @assignment && !@assignment.empty?
    parts << "Capabilities: #{@capabilities.join(", ")}" if @capabilities.any?
    parts.join("\n\n")
  end

  def build_prompt(data = nil)
    template = @config["prompt_template"] || ""
    
    # Replace template variables
    prompt = template.dup
    prompt.gsub!("{{data}}", data.to_s) if data
    prompt.gsub!("{{filter_query}}", @config.dig("gmail", "filter_query").to_s)
    
    prompt
  end

  def send_email_output(output)
    return unless has_tool?("gmail")
    
    profile = @gmail_client.get_user_profile("me")
    user_email = profile.email_address.to_s.strip.gsub(/[<>]/, "")

    subject_template = @config.dig("output", "email", "subject_template") || 
                      "Agent Report #{Time.now.strftime("%Y-%m-%d")}"
    subject_line = subject_template.gsub("{{date}}", Time.now.strftime("%Y-%m-%d"))
    
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

  def extract_message_content(message)
    headers = message.payload.headers

    from = headers.find { |h| h.name.downcase == "from" }&.value || "Unknown"
    subject = headers.find { |h| h.name.downcase == "subject" }&.value || "No Subject"

    thread_id = message.thread_id
    gmail_link = (thread_id ? "https://mail.google.com/mail/u/0/#inbox/#{thread_id}" : nil)

    body_text = get_main_text_part(message.payload)

    "From: #{from}\nSubject: #{subject}\nLink: #{gmail_link}\n\n#{body_text}\n"
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

    if %w[text/plain text/html].include?(payload.mime_type)
      return decode_body(payload.body.data)
    end

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

