#!/bin/bash

files=$(git diff --name-only --diff-filter=ACMR HEAD 2>/dev/null | grep '\.go$')

if [ -z "$files" ]; then
    echo "No changed Go files found"
    exit 0
fi

echo "$files" | xargs golangci-lint fmt
