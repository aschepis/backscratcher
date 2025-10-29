# frozen_string_literal: true

require "net/http"
require "net/https"
require "uri"
require "json"
require "webrick"

namespace :agents do
  desc "Run email digest agent"
  task :email_digest do
    require "email_digest_agent"
    filter_query =
      ENV.fetch(
        "EMAIL_DIGEST_FILTER_QUERY",
        "in:inbox is:unread label:digestme"
      )
    EmailDigestAgent.new(filter_query: filter_query).run
  end
end
