#!/usr/bin/env bash

# Do not enable global errexit, nounset, or pipefail modes. Command outcomes
# that form part of the evidence are captured explicitly.

support_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
if ! source "$support_dir/lib/error-handling.sh"; then
  printf 'error: cannot load support error-handling library\n' >&2
  exit 2
fi

scope=all
case_list=world,system,explicit,preserved,empty-tree
profile_packages=${ARISE_PROFILE_PACKAGES:-sys-apps/coreutils sys-apps/file app-shells/bash}
ceiling=${ARISE_PROFILE_TIMEOUT_SECONDS:-315}
capture_syscalls=false
probe_only=false

usage() {
  echo "usage: $0 [--arise-only] [--syscalls] [--probe-only] [--cases LIST] [--packages 'ATOM ...']" >&2
  echo "cases: world, system, explicit, preserved, empty-tree" >&2
}

while (( $# > 0 )); do
  case $1 in
    --arise-only) scope=arise-only ;;
    --syscalls) capture_syscalls=true ;;
    --probe-only) probe_only=true ;;
    --cases)
      (( $# >= 2 )) || { usage; exit 2; }
      case_list=$2
      shift
      ;;
    --packages)
      (( $# >= 2 )) || { usage; exit 2; }
      profile_packages=$2
      shift
      ;;
    -h|--help) usage; exit 0 ;;
    *) usage; exit 2 ;;
  esac
  shift
done

if ! $probe_only && (( EUID != 0 )); then
  echo "error: run this script as root" >&2
  exit 2
fi
if ! support_require_commands date emerge git go mktemp python3 sha256sum tar timeout uname; then
  exit 2
fi
[[ $ceiling =~ ^[1-9][0-9]*$ ]] || {
  echo "error: ARISE_PROFILE_TIMEOUT_SECONDS must be a positive integer" >&2
  exit 2
}

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
output_dir=$(mktemp -d /tmp/arise-p3-matrix.XXXXXX) || {
  echo "error: cannot create profile output directory" >&2
  exit 1
}
cache_dir=$(mktemp -d /tmp/arise-p3-cache.XXXXXX) || {
  echo "error: cannot create profile cache directory" >&2
  rmdir -- "$output_dir" 2>/dev/null || :
  exit 1
}
cleanup() {
  [[ -z $cache_dir ]] || rm -rf -- "$cache_dir"
}
trap cleanup EXIT
binary=$output_dir/arise-profile
emerge_path=$(command -v emerge)
portage_python=$(command -v python3)
IFS=',' read -r -a requested_cases <<< "$case_list"
read -r -a explicit_atoms <<< "$profile_packages"

have_perf_stat=false
have_perf_record=false
if command -v perf >/dev/null 2>&1; then
  if perf stat -o "$output_dir/perf-stat-probe.txt" -- true >/dev/null 2>&1; then
    have_perf_stat=true
  fi
  if perf record -o "$output_dir/perf-record-probe.data" -- true >/dev/null 2>&1; then
    have_perf_record=true
  fi
fi
rm -f "$output_dir/perf-record-probe.data"
have_strace=false
if command -v strace >/dev/null 2>&1; then
  if strace -c -o "$output_dir/strace-probe.txt" true >/dev/null 2>&1; then
    have_strace=true
  fi
fi

echo "P3 resolver profile matrix"
echo "Repository: $repo_dir"
echo "Output:     $output_dir"
echo "Cases:      ${requested_cases[*]}"
echo "All package-manager operations are pretend-only."

{
  date --iso-8601=seconds
  uname -a
  go version
  emerge --version
  echo "cases=${requested_cases[*]}"
  echo "explicit_packages=${explicit_atoms[*]}"
  echo "ceiling_seconds=$ceiling"
  echo "perf_stat_usable=$have_perf_stat"
  echo "perf_record_usable=$have_perf_record"
  echo "strace_usable=$have_strace"
  echo "syscall_capture_requested=$capture_syscalls"
  git -C "$repo_dir" status --short
  git -C "$repo_dir" rev-parse HEAD
} > "$output_dir/environment.txt" 2>&1

if $probe_only; then
  chmod -R a+rX "$output_dir"
  echo "Capability probe complete: $output_dir"
  cat "$output_dir/environment.txt"
  exit 0
fi

if ! (cd "$repo_dir" && env GOCACHE="$cache_dir" \
  go build -buildvcs=false -trimpath -o "$binary" ./cmd/arise); then
  echo "error: Arise build failed; evidence remains in $output_dir" >&2
  exit 1
fi
sha256sum "$binary" > "$output_dir/arise-profile.sha256"

case_commands() {
  local name=$1
  common_arise=(--pretend --resolver-timeout=5m --json)
  common_portage=(-p --complete-graph --with-bdeps=y --keep-going --backtrack=20)
  case $name in
    world)
      arise_args=("${common_arise[@]}" --update --deep --newuse --complete-graph --with-bdeps=y --keep-going --backtrack=20 install @world)
      portage_args=(-puvDN "${common_portage[@]:1}" @world)
      ;;
    system)
      arise_args=("${common_arise[@]}" --update --deep --newuse --complete-graph --with-bdeps=y --keep-going --backtrack=20 install @system)
      portage_args=(-puvDN "${common_portage[@]:1}" @system)
      ;;
    explicit)
      arise_args=("${common_arise[@]}" --update --deep --newuse --complete-graph --with-bdeps=y --keep-going --backtrack=20 install "${explicit_atoms[@]}")
      portage_args=(-puvDN "${common_portage[@]:1}" "${explicit_atoms[@]}")
      ;;
    preserved)
      arise_args=(--pretend preserved-rebuild)
      portage_args=(-p --keep-going --backtrack=20 @preserved-rebuild)
      ;;
    empty-tree)
      arise_args=("${common_arise[@]}" --emptytree --update --deep --newuse --complete-graph --with-bdeps=y --keep-going --backtrack=20 install @world)
      portage_args=(-peuvDN "${common_portage[@]:1}" @world)
      ;;
    *)
      echo "error: unknown profile case: $name" >&2
      return 2
      ;;
  esac
}

run_arise() {
  local name=$1 case_dir=$2 started finished status
  started=$(date +%s)
  profile_env=(env ARISE_CPU_PROFILE="$case_dir/arise.cpu.pprof" \
    ARISE_GO_TRACE="$case_dir/arise.trace" \
    ARISE_HEAP_PROFILE="$case_dir/arise.heap.pprof" \
    ARISE_ALLOCS_PROFILE="$case_dir/arise.allocs.pprof")
  if $have_perf_stat && $have_perf_record; then
    "${profile_env[@]}" perf stat -d -d -o "$case_dir/arise.perf-stat.txt" -- \
      perf record -o "$case_dir/arise.perf.data" -g --call-graph dwarf -- \
      timeout --signal=INT --kill-after=15s "${ceiling}s" \
      "$binary" "${arise_args[@]}" > "$case_dir/arise.stdout" 2> "$case_dir/arise.stderr"
  elif $have_perf_record; then
    "${profile_env[@]}" perf record -o "$case_dir/arise.perf.data" -g --call-graph dwarf -- \
      timeout --signal=INT --kill-after=15s "${ceiling}s" \
      "$binary" "${arise_args[@]}" > "$case_dir/arise.stdout" 2> "$case_dir/arise.stderr"
  elif $have_perf_stat; then
    "${profile_env[@]}" perf stat -d -d -o "$case_dir/arise.perf-stat.txt" -- \
      timeout --signal=INT --kill-after=15s "${ceiling}s" \
      "$binary" "${arise_args[@]}" > "$case_dir/arise.stdout" 2> "$case_dir/arise.stderr"
  else
    "${profile_env[@]}" timeout --signal=INT --kill-after=15s "${ceiling}s" \
      "$binary" "${arise_args[@]}" > "$case_dir/arise.stdout" 2> "$case_dir/arise.stderr"
  fi
  status=$?
  finished=$(date +%s)
  printf '%s\n' "$status" > "$case_dir/arise.exit"
  printf '%s\n' "$((finished - started))" > "$case_dir/arise.elapsed-seconds"
  if [[ -s $case_dir/arise.cpu.pprof ]]; then
    go tool pprof -top -cum "$binary" "$case_dir/arise.cpu.pprof" > "$case_dir/arise.cpu.cum.txt" 2> "$case_dir/arise.cpu-cum.stderr"
    go tool pprof -top "$binary" "$case_dir/arise.cpu.pprof" > "$case_dir/arise.cpu.flat.txt" 2> "$case_dir/arise.cpu-flat.stderr"
  fi
  for profile_kind in heap allocs; do
    if [[ -s $case_dir/arise.$profile_kind.pprof ]]; then
      go tool pprof -top -cum "$binary" "$case_dir/arise.$profile_kind.pprof" > "$case_dir/arise.$profile_kind.cum.txt" 2> "$case_dir/arise.$profile_kind-cum.stderr"
      go tool pprof -top "$binary" "$case_dir/arise.$profile_kind.pprof" > "$case_dir/arise.$profile_kind.flat.txt" 2> "$case_dir/arise.$profile_kind-flat.stderr"
    fi
  done
  if [[ -s $case_dir/arise.trace ]]; then
    for trace_kind in sched sync syscall; do
      go tool trace -pprof="$trace_kind" "$case_dir/arise.trace" > "$case_dir/arise.trace-$trace_kind.pprof" 2> "$case_dir/arise.trace-$trace_kind.stderr"
      if [[ -s $case_dir/arise.trace-$trace_kind.pprof ]]; then
        go tool pprof -top "$binary" "$case_dir/arise.trace-$trace_kind.pprof" > "$case_dir/arise.trace-$trace_kind.txt" 2>> "$case_dir/arise.trace-$trace_kind.stderr"
      fi
    done
  fi
  if $have_perf_record && [[ -s $case_dir/arise.perf.data ]]; then
    perf report --stdio --percent-limit 0.5 -i "$case_dir/arise.perf.data" > "$case_dir/arise.perf.txt" 2> "$case_dir/arise.perf-report.stderr"
  fi
  echo "$status $((finished - started))"
}

run_portage() {
  local case_dir=$1 started finished status
  if [[ $scope == arise-only ]]; then
    echo "skipped 0"
    return
  fi
  started=$(date +%s)
  if $have_perf_stat && $have_perf_record; then
    env PYTHONUNBUFFERED=1 \
      perf stat -d -d -o "$case_dir/portage.perf-stat.txt" -- \
      perf record -o "$case_dir/portage.perf.data" -g --call-graph dwarf -- \
      timeout --signal=INT --kill-after=15s "${ceiling}s" \
      "$portage_python" -m cProfile -o "$case_dir/portage.cprofile" "$emerge_path" \
      "${portage_args[@]}" > "$case_dir/portage.stdout" 2> "$case_dir/portage.stderr"
  elif $have_perf_record; then
    env PYTHONUNBUFFERED=1 \
      perf record -o "$case_dir/portage.perf.data" -g --call-graph dwarf -- \
      timeout --signal=INT --kill-after=15s "${ceiling}s" \
      "$portage_python" -m cProfile -o "$case_dir/portage.cprofile" "$emerge_path" \
      "${portage_args[@]}" > "$case_dir/portage.stdout" 2> "$case_dir/portage.stderr"
  elif $have_perf_stat; then
    env PYTHONUNBUFFERED=1 perf stat -d -d -o "$case_dir/portage.perf-stat.txt" -- \
      timeout --signal=INT --kill-after=15s "${ceiling}s" \
      "$portage_python" -m cProfile -o "$case_dir/portage.cprofile" "$emerge_path" \
      "${portage_args[@]}" > "$case_dir/portage.stdout" 2> "$case_dir/portage.stderr"
  else
    env PYTHONUNBUFFERED=1 timeout --signal=INT --kill-after=15s "${ceiling}s" \
      "$portage_python" -m cProfile -o "$case_dir/portage.cprofile" "$emerge_path" \
      "${portage_args[@]}" > "$case_dir/portage.stdout" 2> "$case_dir/portage.stderr"
  fi
  status=$?
  finished=$(date +%s)
  printf '%s\n' "$status" > "$case_dir/portage.exit"
  printf '%s\n' "$((finished - started))" > "$case_dir/portage.elapsed-seconds"
  if [[ -s $case_dir/portage.cprofile ]]; then
    "$portage_python" -c 'import pstats,sys; pstats.Stats(sys.argv[1]).strip_dirs().sort_stats("cumulative").print_stats()' \
      "$case_dir/portage.cprofile" > "$case_dir/portage.cprofile.cum.txt" 2> "$case_dir/portage.cprofile-report.stderr"
  fi
  if $have_perf_record && [[ -s $case_dir/portage.perf.data ]]; then
    perf report --stdio --percent-limit 0.5 -i "$case_dir/portage.perf.data" > "$case_dir/portage.perf.txt" 2> "$case_dir/portage.perf-report.stderr"
  fi
  echo "$status $((finished - started))"
}

run_syscall_summaries() {
  local case_dir=$1
  if ! $capture_syscalls; then
    printf '%s\n' "not requested" > "$case_dir/syscalls.status"
    return
  fi
  if ! $have_strace; then
    printf '%s\n' "skipped: strace unavailable or unusable" > "$case_dir/syscalls.status"
    return
  fi
  printf '%s\n' "captured" > "$case_dir/syscalls.status"
  timeout --signal=INT --kill-after=15s "${ceiling}s" \
    strace -f -c -o "$case_dir/arise.strace-summary.txt" \
    "$binary" "${arise_args[@]}" > "$case_dir/arise.strace.stdout" 2> "$case_dir/arise.strace.stderr"
  printf '%s\n' "$?" > "$case_dir/arise.strace.exit"
  if [[ $scope != arise-only ]]; then
    env PYTHONUNBUFFERED=1 timeout --signal=INT --kill-after=15s "${ceiling}s" \
      strace -f -c -o "$case_dir/portage.strace-summary.txt" \
      "$emerge_path" "${portage_args[@]}" > "$case_dir/portage.strace.stdout" 2> "$case_dir/portage.strace.stderr"
    printf '%s\n' "$?" > "$case_dir/portage.strace.exit"
  fi
}

summary=$output_dir/summary.tsv
printf 'case\tarise_exit\tarise_seconds\tportage_exit\tportage_seconds\n' > "$summary"
for profile_case in "${requested_cases[@]}"; do
  case_commands "$profile_case" || exit $?
  case_dir=$output_dir/$profile_case
  mkdir -p "$case_dir"
  printf '%q ' "$binary" "${arise_args[@]}" > "$case_dir/arise.command"
  printf '\n' >> "$case_dir/arise.command"
  printf '%q ' emerge "${portage_args[@]}" > "$case_dir/portage.command"
  printf '\n' >> "$case_dir/portage.command"
  echo "Profiling $profile_case (per-command ceiling: ${ceiling}s)"
  read -r arise_status arise_seconds <<< "$(run_arise "$profile_case" "$case_dir")"
  read -r portage_status portage_seconds <<< "$(run_portage "$case_dir")"
  run_syscall_summaries "$case_dir"
  printf '%s\t%s\t%s\t%s\t%s\n' "$profile_case" "$arise_status" "$arise_seconds" "$portage_status" "$portage_seconds" >> "$summary"
  echo "  Arise: $arise_status/${arise_seconds}s; Portage: $portage_status/${portage_seconds}s"
done

chmod -R a+rX "$output_dir"
archive=$output_dir.tar.gz
tar -C "$(dirname -- "$output_dir")" -czf "$archive" "$(basename -- "$output_dir")"
chmod a+r "$archive"
echo "Complete: $output_dir"
echo "Archive:  $archive"
cat "$summary"
