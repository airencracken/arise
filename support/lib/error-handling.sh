#!/usr/bin/env bash

# Shared explicit error-handling primitives for first-party support tools.
# This library deliberately changes no global shell options and never exits.

support_error() {
  printf 'error: %s\n' "$*" >&2
}

support_have_command() {
  command -v "$1" >/dev/null 2>&1
}

support_require_commands() {
  local command_name missing=0
  for command_name in "$@"; do
    if ! support_have_command "$command_name"; then
      support_error "required command not found: $command_name"
      missing=1
    fi
  done
  return "$missing"
}

support_run_required() {
  local description=$1
  shift
  "$@"
  local status=$?
  if (( status != 0 )); then
    support_error "$description failed with status $status"
  fi
  return "$status"
}
