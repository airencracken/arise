#!/usr/bin/env bash
set -u
set -o pipefail

scope=${1:-all}
if [[ $scope != all && $scope != --arise-only ]]; then
  echo "usage: $0 [--arise-only]" >&2
  exit 2
fi

if (( EUID != 0 )); then
  echo "error: run this script as root" >&2
  exit 2
fi

for command in go timeout emerge python3; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "error: required command not found: $command" >&2
    exit 2
  fi
done

have_perf=false
if command -v perf >/dev/null 2>&1; then
  have_perf=true
fi
emerge_path=$(command -v emerge)
portage_python=$(command -v python3)

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
output_dir=$(mktemp -d /tmp/arise-p3-profile.XXXXXX)
binary=$output_dir/arise-profile

echo "P3 world resolver profile"
echo "Repository: $repo_dir"
echo "Output:     $output_dir"
echo "Both package-manager operations are pretend-only."

echo "[1/7] Recording environment"
{
  date --iso-8601=seconds
  uname -a
  go version
  if $have_perf; then perf --version; else echo "perf: unavailable (using language-native profilers)"; fi
  echo "portage_python=$portage_python"
  emerge --version
  git -C "$repo_dir" status --short
  git -C "$repo_dir" rev-parse HEAD
} > "$output_dir/environment.txt" 2>&1

echo "[2/7] Building current Arise source"
if ! (cd "$repo_dir" && env GOCACHE=/tmp/arise-p3-go-cache-root \
  go build -buildvcs=false -trimpath -o "$binary" ./cmd/arise); then
  echo "error: Arise build failed; evidence remains in $output_dir" >&2
  exit 1
fi
sha256sum "$binary" > "$output_dir/arise-profile.sha256"

echo "[3/7] Profiling Arise (hard ceiling: 315 seconds)"
arise_started=$(date +%s)
if $have_perf; then
  env ARISE_CPU_PROFILE="$output_dir/arise.pprof" \
    perf record -o "$output_dir/arise.perf.data" -g --call-graph dwarf -- \
    timeout --signal=INT --kill-after=15s 315s \
    "$binary" \
      --pretend \
      --update \
      --deep \
      --newuse \
      --complete-graph \
      --with-bdeps=y \
      --keep-going \
      --backtrack=20 \
      --resolver-timeout=5m \
      --json \
      install @world > "$output_dir/arise.json" 2> "$output_dir/arise.stderr"
else
  env ARISE_CPU_PROFILE="$output_dir/arise.pprof" \
    timeout --signal=INT --kill-after=15s 315s \
    "$binary" \
      --pretend --update --deep --newuse --complete-graph \
      --with-bdeps=y --keep-going --backtrack=20 \
      --resolver-timeout=5m --json install @world \
    > "$output_dir/arise.json" 2> "$output_dir/arise.stderr"
fi
arise_status=$?
arise_finished=$(date +%s)
printf '%s\n' "$arise_status" > "$output_dir/arise.exit"
printf '%s\n' "$((arise_finished - arise_started))" > "$output_dir/arise.elapsed-seconds"
echo "      Arise exit=$arise_status elapsed=$((arise_finished - arise_started))s"

echo "[4/7] Profiling Portage without --ask (hard ceiling: 315 seconds)"
portage_started=$(date +%s)
portage_status=skipped
portage_finished=$portage_started
if [[ $scope == --arise-only ]]; then
  echo "      Portage skipped by --arise-only"
elif $have_perf; then
  env PYTHONUNBUFFERED=1 \
    perf record -o "$output_dir/portage.perf.data" -g --call-graph dwarf -- \
    timeout --signal=INT --kill-after=15s 315s \
    "$portage_python" -m cProfile -o "$output_dir/portage.cprofile" "$emerge_path" \
      -puvDN \
      --complete-graph \
      --with-bdeps=y \
      --keep-going \
      --backtrack=20 @world > "$output_dir/portage.stdout" 2> "$output_dir/portage.stderr"
else
  env PYTHONUNBUFFERED=1 \
    timeout --signal=INT --kill-after=15s 315s \
    "$portage_python" -m cProfile -o "$output_dir/portage.cprofile" "$emerge_path" \
      -puvDN --complete-graph --with-bdeps=y --keep-going --backtrack=20 @world \
    > "$output_dir/portage.stdout" 2> "$output_dir/portage.stderr"
fi
if [[ $scope != --arise-only ]]; then
  portage_status=$?
  portage_finished=$(date +%s)
fi
printf '%s\n' "$portage_status" > "$output_dir/portage.exit"
printf '%s\n' "$((portage_finished - portage_started))" > "$output_dir/portage.elapsed-seconds"
echo "      Portage exit=$portage_status elapsed=$((portage_finished - portage_started))s"

echo "[5/7] Generating perf reports"
if $have_perf && [[ -s $output_dir/arise.perf.data ]]; then
  perf report --stdio --percent-limit 0.5 \
    -i "$output_dir/arise.perf.data" \
    > "$output_dir/arise.perf.txt" 2> "$output_dir/arise.perf-report.stderr"
fi
if $have_perf && [[ -s $output_dir/portage.perf.data ]]; then
  perf report --stdio --percent-limit 0.5 \
    -i "$output_dir/portage.perf.data" \
    > "$output_dir/portage.perf.txt" 2> "$output_dir/portage.perf-report.stderr"
fi
if [[ -s $output_dir/portage.cprofile ]]; then
  "$portage_python" -c 'import pstats,sys; pstats.Stats(sys.argv[1]).strip_dirs().sort_stats("cumulative").print_stats()' \
    "$output_dir/portage.cprofile" > "$output_dir/portage.cprofile.cum.txt" \
    2> "$output_dir/portage.cprofile-report.stderr"
fi

echo "[6/7] Generating Go CPU reports"
if [[ -s $output_dir/arise.pprof ]]; then
  go tool pprof -top -cum "$binary" "$output_dir/arise.pprof" \
    > "$output_dir/arise.pprof.cum.txt" 2> "$output_dir/arise.pprof-cum.stderr"
  go tool pprof -top "$binary" "$output_dir/arise.pprof" \
    > "$output_dir/arise.pprof.flat.txt" 2> "$output_dir/arise.pprof-flat.stderr"
fi

echo "[7/7] Packaging evidence"
summary=$output_dir/summary.txt
{
  echo "output_dir=$output_dir"
  echo "arise_exit=$arise_status"
  echo "arise_elapsed_seconds=$((arise_finished - arise_started))"
  echo "portage_exit=$portage_status"
  echo "portage_elapsed_seconds=$((portage_finished - portage_started))"
  echo
  du -ah "$output_dir" | sort -h
} > "$summary"

chmod -R a+rX "$output_dir"
archive=$output_dir.tar.gz
tar -C "$(dirname -- "$output_dir")" -czf "$archive" "$(basename -- "$output_dir")"
chmod a+r "$archive"

echo
echo "Complete."
echo "Evidence directory: $output_dir"
echo "Archive:            $archive"
echo "Summary:"
cat "$summary"
