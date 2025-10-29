#!/usr/bin/env ruby
# frozen_string_literal: true

# Add agents/lib to the load path
$LOAD_PATH.unshift(File.expand_path("agents/lib", __dir__))

# Load all rake tasks from lib/tasks
Dir.glob("agents/lib/tasks/*.rake").each { |file| load file }
