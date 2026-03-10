# frozen_string_literal: true

namespace :agents do
  desc "Analyze inbox and suggest Gmail filter rules for digestable emails"
  task :email_filter do
    require "email_filter_agent"
    days = ENV.fetch("EMAIL_FILTER_DAYS", 30).to_i
    EmailFilterAgent.new(days: days).run
  end
end
