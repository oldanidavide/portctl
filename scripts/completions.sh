#!/bin/sh
# Writes the shell completion scripts to completions/, for the release packages.
# zsh only loads completion files named after the command with a leading "_".
set -e
rm -rf completions
mkdir completions
go run ./cmd/portctl completion bash > completions/portctl.bash
go run ./cmd/portctl completion zsh > completions/_portctl
go run ./cmd/portctl completion fish > completions/portctl.fish
