# frozen_string_literal: true

require "net/http"
require "net/https"
require "uri"
require "json"
require "webrick"

namespace :agents do
  desc "Run email digest agent (sends one digest per digestme sub-label)"
  task :email_digest do
    require "email_digest_agent"
    EmailDigestAgent.new.run
  end
end
