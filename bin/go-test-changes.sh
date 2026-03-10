#!/bin/bash

clean=false
if [[ "$1" == "--clean" ]]; then
    clean=true
fi

  # Get all changed .go files (staged and unstaged)
  changed_go_files=$( (git diff --cached --name-only; git diff --name-only) | grep '\.go$' | sort -u)

  if [ -z "$changed_go_files" ]; then
      echo "No changed Go files found"
      exit 0
  fi

  # For non-test .go files, find their corresponding _test.go files
  test_files=$(echo "$changed_go_files" | while read -r f; do
      if echo "$f" | grep -q '_test\.go$'; then
          echo "$f"
      else
          test_file="${f%.go}_test.go"
          if [ -f "$test_file" ]; then
              echo "$test_file"
          fi
      fi
  done | sort -u)

  if [ -z "$test_files" ]; then
      echo "No changed test files found"
      exit 0
  fi

  # Extract test function names
  test_names=$(echo "$test_files" | xargs grep -h '^func Test' 2>/dev/null | sed 's/func \(Test[^(]*\).*/\1/' | sort -u)

  if [ -z "$test_names" ]; then
      echo "No test functions found in changed files"
      exit 0
  fi

  # Get unique directories
  test_dirs=$(echo "$test_files" | xargs dirname | sort -u | sed 's|^|./|' | xargs)

  # Build the regex pattern
  test_pattern=$(echo "$test_names" | tr '\n' '|' | sed 's/|$//')

  cmd="go test -v -count=1 $test_dirs -run \"^($test_pattern)\$\""
  echo "$cmd"

  if $clean; then
      eval "$cmd" | grep --line-buffered -v '{"status'
  else
      eval "$cmd"
  fi
