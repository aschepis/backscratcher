#!/bin/bash

grep --line-buffered -Ev \
  -e '^\{"status"' \
  -e '^\{.*"level":' \
  -e '^time="' \
  -e 'level=(debug|info|warn|warning|error|fatal|panic)' \
  -e '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}[^[:blank:]]*[[:blank:]]+(DEBUG|INFO|WARN|WARNING|ERROR|FATAL|PANIC|DPANIC)' \
  -e '^[0-9]{4}/[0-9]{2}/[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}'
