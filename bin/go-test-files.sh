#!/bin/bash

clean=false
args=()
for arg in "$@"; do
    if [[ "$arg" == "--clean" ]]; then
        clean=true
    else
        args+=("$arg")
    fi
done

if [ ${#args[@]} -eq 0 ]; then
    echo "Usage: $(basename "$0") [--clean] <file1> [file2] ..."
    exit 1
fi

test_files=""
errors=""

for file in "${args[@]}"; do
    # If it doesn't end in _test.go, look for the matching test file
    if [[ "$file" != *_test.go ]]; then
        test_file="${file%.go}_test.go"
    else
        test_file="$file"
    fi

    if [ ! -f "$test_file" ]; then
        errors="$errors\nError: test file not found: $test_file"
        continue
    fi

    test_files="$test_files $test_file"
done

if [ -n "$errors" ]; then
    echo -e "$errors" >&2
    exit 1
fi

if [ -z "$test_files" ]; then
    echo "No test files found"
    exit 1
fi

# Extract test function names
test_names=$(echo "$test_files" | xargs grep -h '^func Test' 2>/dev/null | sed 's/func \(Test[^(]*\).*/\1/' | sort -u)

if [ -z "$test_names" ]; then
    echo "No test functions found in specified files"
    exit 0
fi

# Get unique directories
test_dirs=$(echo "$test_files" | tr ' ' '\n' | xargs dirname | sort -u | sed 's|^|./|' | xargs)

# Build the regex pattern
test_pattern=$(echo "$test_names" | tr '\n' '|' | sed 's/|$//')

cmd="go test -v -count=1 $test_dirs -run \"^($test_pattern)\$\""
echo "$cmd"

if $clean; then
    eval "$cmd" | grep --line-buffered -v '{"status'
else
    eval "$cmd"
fi
