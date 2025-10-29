# frozen_string_literal: true

require "base_agent"
require "anthropic"

# Agent that uses a prompt to generate output from an LLM
class PromptAgent < BaseAgent
  def initialize(prompt:)
    super()
    @prompt = prompt
    @anthropic_client =
      Anthropic::Client.new(api_key: ENV.fetch("ANTHROPIC_API_KEY", nil))
  end

  def process_data(data = nil)
    stream =
      @anthropic_client.messages.stream(
        model: "claude-sonnet-4-5-20250929",
        max_tokens: 32_000,
        messages: [{ role: "user", content: "#{@prompt}\n\n#{data}" }]
      )

    result = +"" # Creates a mutable string
    stream.text.each do |text|
      @logger.info("Received text chunk: #{text}")
      result << text
    end

    result
  end
end
