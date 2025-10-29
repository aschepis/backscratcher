# frozen_string_literal: true

require "google/apis/gmail_v1"
require "googleauth"
require "net/http"
require "json"
require "base64"

# Base class for all agents
class BaseAgent
  def initialize
    @logger = Logger.new($stdout)
  end

  def name
    self.class.name
  end

  def run
    @logger.info("Running agent #{name}")
    data = fetch_data
    processed_data = process_data(data)
    generate_output(processed_data)
  end

  def fetch_data
    @logger.info("Fetching data")
    raise NotImplementedError, "Subclasses must implement this method"
  end

  def process_data
    @logger.info("Processing data")
    raise NotImplementedError, "Subclasses must implement this method"
  end

  def generate_output
    @logger.info("Generating output")
    raise NotImplementedError, "Subclasses must implement this method"
  end
end
