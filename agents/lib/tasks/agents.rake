# frozen_string_literal: true

require "net/http"
require "net/https"
require "uri"
require "json"
require "webrick"

namespace :agents do
  desc "Run email digest agent"
  task :email_digest do
    require "agent"
    
    config_path = File.join(__dir__, "..", "..", "config", "email_digest_agent.yaml")
    
    # Allow override of filter query via environment variable
    overrides = {}
    if ENV["EMAIL_DIGEST_FILTER_QUERY"]
      overrides["gmail"] = { "filter_query" => ENV["EMAIL_DIGEST_FILTER_QUERY"] }
    end
    
    agent = Agent.new(config_path: config_path, config_overrides: overrides)
    agent.run
  end

  desc "Run grades agent"
  task :grades do
    require "agent"
    
    config_path = File.join(__dir__, "..", "..", "config", "grades_agent.yaml")
    agent = Agent.new(config_path: config_path)
    agent.run
  end

  desc "Run agent by config file"
  task :run, [:config_file] do |_t, args|
    require "agent"
    
    config_path = args[:config_file] || raise("Usage: rake agents:run[path/to/config.yaml]")
    agent = Agent.new(config_path: config_path)
    agent.run
  end
end
