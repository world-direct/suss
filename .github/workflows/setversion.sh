#!/bin/sh

set -e

# check for CI so that this is not called locally
# https://docs.github.com/en/actions/learn-github-actions/variables#default-environment-variables
if [[ -z "$CI" ]]; then
    exit 0
fi

echo "package main" > ./version.go
echo "const VERSION = \"$1\"" >> ./version.go
