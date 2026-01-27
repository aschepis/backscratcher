# frozen_string_literal: true

require "base_agent"
require "anthropic"

# Agent that uses a prompt to generate output from an LLM
class PromptAgent < BaseAgent
  MAX_INPUT_TOKENS = 200_000

  def initialize(prompt:)
    super()
    @prompt = prompt
    @anthropic_client =
      Anthropic::Client.new(api_key: ENV.fetch("ANTHROPIC_API_KEY", nil))
  end

  def process_data(data = nil)
    data_str = data.to_s
    content = "#{@prompt}\n\n#{data_str}"

    begin
      stream =
        @anthropic_client.messages.stream(
          model: "claude-sonnet-4-5-20250929",
          max_tokens: 32_000,
          messages: [{ role: "user", content: content }]
        )

      result = +"" # Creates a mutable string
      stream.text.each { |text| result << text }
      result
    rescue Anthropic::Errors::BadRequestError => e
      raise unless prompt_too_long_error?(e)

      # Extract token information from the error
      prompt_tokens, actual_tokens, max_tokens = parse_prompt_too_long_tokens(e)
      max_tokens ||= MAX_INPUT_TOKENS
      actual_tokens ||= max_tokens + 1

      # Calculate truncation ratio based on actual token counts from the error
      # If we're at 200132 tokens and need to be under 200000, we use the actual ratio
      # Use a larger safety margin to account for token estimation inaccuracy
      safety = 20_000
      target_total_tokens = max_tokens - safety

      # Calculate the ratio: target / actual
      # This gives us the proportion we need to reduce to
      # Be more aggressive - use a lower minimum ratio
      truncation_ratio = [target_total_tokens.to_f / actual_tokens, 0.05].max

      prompt_tokens ||= approx_input_tokens(@prompt.to_s)
      estimated_current_data_tokens = [actual_tokens - prompt_tokens, 0].max
      target_data_tokens = [
        (estimated_current_data_tokens * truncation_ratio).floor,
        0
      ].max

      # Apply an additional safety factor - reduce by another 10% to be safe
      target_data_tokens = (target_data_tokens * 0.9).floor

      @logger.info(
        "Truncating: actual=#{actual_tokens}, max=#{max_tokens}, target_total=#{target_total_tokens}, ratio=#{truncation_ratio.round(3)}, target_data=#{target_data_tokens}"
      )

      trimmed_data =
        if target_data_tokens <= 0
          ""
        else
          truncate_to_approx_tokens(data_str, target_data_tokens)
        end

      # Log truncation details for debugging
      original_size = data_str.bytesize
      trimmed_size = trimmed_data.bytesize
      original_est_tokens = approx_input_tokens(data_str)
      trimmed_est_tokens = approx_input_tokens(trimmed_data)
      @logger.info(
        "Truncation: original=#{original_size} bytes (#{original_est_tokens} est tokens), trimmed=#{trimmed_size} bytes (#{trimmed_est_tokens} est tokens), reduction=#{((1.0 - trimmed_size.to_f / original_size) * 100).round(1)}%"
      )

      retry_content = "#{@prompt}\n\n#{trimmed_data}"
      retry_est_tokens = approx_input_tokens(retry_content)
      @logger.info("Retry content estimated tokens: #{retry_est_tokens}")

      # Retry with truncated content
      begin
        stream =
          @anthropic_client.messages.stream(
            model: "claude-sonnet-4-5-20250929",
            max_tokens: 32_000,
            messages: [{ role: "user", content: retry_content }]
          )

        result = +""
        stream.text.each { |text| result << text }
        result
      rescue Anthropic::Errors::BadRequestError => retry_error
        # If retry still fails, truncate more aggressively using iterative approach
        if prompt_too_long_error?(retry_error)
          @logger.warn("Retry also failed, truncating more aggressively")

          # Keep truncating until we're well under the limit
          current_data = trimmed_data
          retry_count = 0
          max_retries = 5

          loop do
            retry_count += 1
            raise "Too many retry attempts" if retry_count > max_retries

            prompt_tokens, actual_tokens, max_tokens =
              parse_prompt_too_long_tokens(retry_error)
            max_tokens ||= MAX_INPUT_TOKENS
            actual_tokens ||= max_tokens + 1

            # Calculate new target - be much more aggressive using ratio
            safety = 40_000 + (retry_count * 15_000)
            target_total_tokens = max_tokens - safety

            # Use ratio-based truncation for more accurate reduction
            # Be very aggressive - reduce to at most 5% of original, or use calculated ratio
            truncation_ratio = [
              target_total_tokens.to_f / actual_tokens,
              0.03
            ].max

            prompt_tokens ||= approx_input_tokens(@prompt.to_s)
            estimated_current_data_tokens = [
              actual_tokens - prompt_tokens,
              0
            ].max
            target_data_tokens = [
              (estimated_current_data_tokens * truncation_ratio).floor,
              0
            ].max

            # Apply additional safety factor - reduce by another 15% each retry
            safety_factor = 1.0 - (0.15 * retry_count)
            safety_factor = [safety_factor, 0.5].max # Don't go below 50%
            target_data_tokens = (target_data_tokens * safety_factor).floor

            @logger.warn(
              "Aggressive truncation attempt #{retry_count}: actual=#{actual_tokens}, target_total=#{target_total_tokens}, ratio=#{truncation_ratio.round(3)}, target_data=#{target_data_tokens}"
            )

            current_data_before = current_data
            current_data =
              truncate_to_approx_tokens(current_data, target_data_tokens)

            # Log truncation details
            before_size = current_data_before.bytesize
            after_size = current_data.bytesize
            before_tokens = approx_input_tokens(current_data_before)
            after_tokens = approx_input_tokens(current_data)
            @logger.warn(
              "Truncation: before=#{before_size} bytes (#{before_tokens} est tokens), after=#{after_size} bytes (#{after_tokens} est tokens), reduction=#{((1.0 - after_size.to_f / before_size) * 100).round(1)}%"
            )

            retry_content = "#{@prompt}\n\n#{current_data}"
            retry_est_tokens = approx_input_tokens(retry_content)
            @logger.warn("Retry content estimated tokens: #{retry_est_tokens}")

            begin
              stream =
                @anthropic_client.messages.stream(
                  model: "claude-sonnet-4-5-20250929",
                  max_tokens: 32_000,
                  messages: [{ role: "user", content: retry_content }]
                )

              result = +""
              stream.text.each { |text| result << text }
              return result
            rescue Anthropic::Errors::BadRequestError => inner_error
              if prompt_too_long_error?(inner_error)
                # Update retry_error with the new error for next iteration
                retry_error = inner_error
                # Recalculate with new error values
                next
              else
                raise
              end
            end
          end
        else
          raise
        end
      end
    end
  end

  private

  def prompt_too_long_error?(error)
    # Check error.message first
    msg = error.message.to_s
    if msg.include?("prompt is too long") &&
         (msg.include?("maximum") || msg.include?("tokens"))
      return true
    end

    # Check error.to_s / inspect which might contain the full error details
    error_str = error.to_s
    if error_str.include?("prompt is too long") &&
         (error_str.include?("maximum") || error_str.include?("tokens"))
      return true
    end

    # Check error.inspect which might have the full structure
    error_inspect = error.inspect
    if error_inspect.include?("prompt is too long") &&
         (error_inspect.include?("maximum") || error_inspect.include?("tokens"))
      return true
    end

    # Check error.body if available (error.body might be a Hash or String)
    if error.respond_to?(:body)
      body = error.body
      body_str = body.is_a?(Hash) ? body.to_json : body.to_s
      if body_str.include?("prompt is too long") &&
           (body_str.include?("maximum") || body_str.include?("tokens"))
        return true
      end
    end

    # Check error.response if available
    if error.respond_to?(:response) && error.response.respond_to?(:body)
      resp_body = error.response.body
      resp_str = resp_body.is_a?(Hash) ? resp_body.to_json : resp_body.to_s
      if resp_str.include?("prompt is too long") &&
           (resp_str.include?("maximum") || resp_str.include?("tokens"))
        return true
      end
    end

    false
  end

  # Example:
  #   "prompt is too long: 200691 tokens > 200000 maximum"
  #   Error body: {type: "error", error: {type: "invalid_request_error", message: "prompt is too long: 200690 tokens > 200000 maximum"}}
  def parse_prompt_too_long_tokens(error)
    # Helper to extract tokens from a string
    extract_tokens =
      lambda do |text|
        m =
          text.to_s.match(
            /prompt is too long:\s*(\d+)\s*tokens\s*>\s*(\d+)\s*maximum/i
          )
        return m[1].to_i, m[2].to_i if m

        nil
      end

    # Try to extract from error.message first
    msg = error.message.to_s
    tokens = extract_tokens.call(msg)
    return nil, tokens[0], tokens[1] if tokens

    # Try error.to_s and inspect which might contain the full error details
    error_str = error.to_s
    tokens = extract_tokens.call(error_str)
    return nil, tokens[0], tokens[1] if tokens

    error_inspect = error.inspect
    tokens = extract_tokens.call(error_inspect)
    return nil, tokens[0], tokens[1] if tokens

    # Try to extract from error.body - check nested structure first
    if error.respond_to?(:body)
      body = error.body

      # Check nested error structure: body[:error][:message] or body["error"]["message"]
      if body.is_a?(Hash)
        error_obj = body[:error] || body["error"]
        if error_obj.is_a?(Hash)
          error_msg = error_obj[:message] || error_obj["message"]
          if error_msg
            tokens = extract_tokens.call(error_msg)
            return nil, tokens[0], tokens[1] if tokens
          end
        end
      end

      # Try body as string/json
      body_str = body.is_a?(Hash) ? body.to_json : body.to_s
      tokens = extract_tokens.call(body_str)
      return nil, tokens[0], tokens[1] if tokens
    end

    # Try error.response.body if available
    if error.respond_to?(:response) && error.response.respond_to?(:body)
      resp_body = error.response.body

      if resp_body.is_a?(Hash)
        error_obj = resp_body[:error] || resp_body["error"]
        if error_obj.is_a?(Hash)
          error_msg = error_obj[:message] || error_obj["message"]
          if error_msg
            tokens = extract_tokens.call(error_msg)
            return nil, tokens[0], tokens[1] if tokens
          end
        end
      end

      resp_str = resp_body.is_a?(Hash) ? resp_body.to_json : resp_body.to_s
      tokens = extract_tokens.call(resp_str)
      return nil, tokens[0], tokens[1] if tokens
    end

    # Log if we couldn't extract tokens (for debugging)
    @logger.warn(
      "Could not extract token counts from error: #{error.class} - #{error.message[0..200]}"
    )

    [nil, nil, nil]
  end

  # Conservative input-token approximation for Anthropic.
  def approx_input_tokens(text)
    return 0 if text.nil? || text.empty?
    (text.to_s.bytesize / 3.5).ceil
  end

  def truncate_to_approx_tokens(text, token_budget)
    return "" if token_budget <= 0
    max_bytes = (token_budget * 3.5).floor
    s = text.to_s
    return s if s.bytesize <= max_bytes

    # Truncate to max_bytes, but ensure we actually reduce the size
    truncated = s.byteslice(0, max_bytes).to_s.force_encoding("UTF-8").scrub

    # Safety check: if truncation didn't actually reduce size (shouldn't happen, but just in case),
    # force a more aggressive truncation
    if truncated.bytesize >= s.bytesize && s.bytesize > 0
      @logger.warn("Truncation didn't reduce size, forcing more aggressive cut")
      # Force to 80% of target to be extra safe
      max_bytes = (max_bytes * 0.8).floor
      truncated = s.byteslice(0, max_bytes).to_s.force_encoding("UTF-8").scrub
    end

    truncated
  end
end
