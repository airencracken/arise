#!/bin/bash

# Read-only reference capture for Arise parity fixtures. This script deliberately
# avoids errexit/nounset/pipefail and checks every operation explicitly.

support_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
if ! source "$support_dir/lib/error-handling.sh"; then
	printf 'capture: cannot load support error-handling library\n' >&2
	exit 2
fi

output=${1-}
mode=${ARISE_CAPTURE_MODE-standard}
targets_raw=${ARISE_CAPTURE_TARGETS-"sys-apps/portage app-shells/bash net-im/signal-desktop-bin"}
case_timeout=${ARISE_CAPTURE_CASE_TIMEOUT-120}
environment_probe=${ARISE_CAPTURE_ENV_PROBE-0}

if [[ $environment_probe == 1 ]]; then
	export USE='-doc'
	export FEATURES='-test'
	export ACCEPT_KEYWORDS='~amd64'
	export ACCEPT_LICENSE='* -@EULA'
	export CFLAGS='-O0 -pipe'
	export CXXFLAGS='-O0 -pipe'
	export LDFLAGS='-Wl,-O1'
	export MAKEOPTS='-j3'
	export NOCOLOR='true'
	export COLUMNS='117'
	export PORTAGE_NICENESS='7'
fi

if [[ -z $output ]]; then
	printf 'usage: %s OUTPUT_DIRECTORY\n' "$0" >&2
	exit 2
fi
if [[ -e $output ]]; then
	printf 'capture: output already exists: %s\n' "$output" >&2
	exit 2
fi
if ! mkdir -p "$output/cases"; then
	printf 'capture: cannot create output: %s\n' "$output" >&2
	exit 1
fi

case_index=0
failures=0

run_case() {
	local name=$1
	shift
	local prefix display_index=$case_index
	printf -v prefix '%s/cases/%03d-%s' "$output" "$case_index" "$name"
	case_index=$((case_index + 1))
	printf '%q ' "$@" > "${prefix}.command"
	printf '\n' >> "${prefix}.command"
	local status started=$SECONDS case_pid frame_index=0
	local frames='|/-\\'
	printf 'capture [%03d] %s ... ' "$display_index" "$name" >&2
	if have timeout; then
		if [[ -t 2 ]]; then
			timeout --foreground "${case_timeout}s" "$@" > "${prefix}.stdout" 2> "${prefix}.stderr" &
			case_pid=$!
			while kill -0 "$case_pid" 2>/dev/null; do
				printf '\r\033[Kcapture [%03d] %s ... %s %ds' "$display_index" "$name" "${frames:frame_index++%4:1}" "$((SECONDS-started))" >&2
				sleep 0.1
			done
			wait "$case_pid"
			status=$?
			printf '\r\033[K' >&2
		else
			timeout --foreground "${case_timeout}s" "$@" > "${prefix}.stdout" 2> "${prefix}.stderr"
			status=$?
		fi
	else
		"$@" > "${prefix}.stdout" 2> "${prefix}.stderr"
		status=$?
	fi
	printf '%d\n' "$status" > "${prefix}.status"
	if [[ -t 2 ]]; then
		printf 'capture [%03d] %s ... status %d (%ds)\n' "$display_index" "$name" "$status" "$((SECONDS-started))" >&2
	else
		printf 'status %d (%ds)\n' "$status" "$((SECONDS-started))" >&2
	fi
	if (( status != 0 )); then
		failures=$((failures + 1))
	fi
	return 0
}

have() { support_have_command "$1"; }

if [[ ! $case_timeout =~ ^[1-9][0-9]*$ ]]; then
	printf 'capture: ARISE_CAPTURE_CASE_TIMEOUT must be a positive integer\n' >&2
	exit 2
fi

printf 'schema=1\nmode=%s\nenvironment_probe=%s\nstarted_utc=%(%Y-%m-%dT%H:%M:%SZ)T\nuid=%s\n' \
	"$mode" "$environment_probe" -1 "$(id -u)" > "$output/capture.meta"

for tool in emerge portageq dispatch-conf etc-update equery euse eshowkw epkginfo enalyze ekeyword ebump eselect q qatom qdepends qfile qlist qsearch qcheck qkeyword qlop qmanifest qsize quse eix eix-update eix-installed eix-installed-after eix-test-obsolete emaint eclean genlop emlop golop glsa-check revdep-rebuild gemato; do
	if have "$tool"; then
		printf '%s=%s\n' "$tool" "$(command -v "$tool")" >> "$output/tools.meta"
	else
		printf '%s=missing\n' "$tool" >> "$output/tools.meta"
	fi
done

run_case uname uname -a
run_case os_release sed -n '1,120p' /etc/os-release
have emerge && run_case emerge_version emerge --version
have portageq && run_case portageq_version portageq --version
have dispatch-conf && run_case dispatch_conf_version dispatch-conf --version
have dispatch-conf && run_case dispatch_conf_help dispatch-conf --help
have etc-update && run_case etc_update_version etc-update --version
have etc-update && run_case etc_update_help etc-update --help
have equery && run_case equery_version equery --version
have euse && run_case euse_version euse --version
have eshowkw && run_case eshowkw_version eshowkw --version
have epkginfo && run_case epkginfo_help epkginfo --help
have enalyze && run_case enalyze_help enalyze --help
have ekeyword && run_case ekeyword_help ekeyword --help
have ebump && run_case ebump_version ebump --version
have eselect && run_case eselect_version eselect --version
have eselect && run_case eselect_modules eselect modules list
have q && run_case q_version q --version
have eix && run_case eix_version eix --version
have genlop && run_case genlop_version genlop -v
have emlop && run_case emlop_version emlop --version
have emlop && run_case emlop_help emlop --help
have golop && run_case golop_help golop -h
have qlop && run_case qlop_version qlop --version
have gemato && run_case gemato_help gemato --help

if [[ $mode == smoke ]]; then
	printf 'cases=%d\nfailures=%d\n' "$case_index" "$failures" >> "$output/capture.meta"
	exit 0
fi

if have portageq; then
	run_case portageq_env portageq envvar ARCH USE FEATURES ACCEPT_KEYWORDS ACCEPT_LICENSE CHOST CBUILD CTARGET ROOT SYSROOT BROOT DISTDIR PKGDIR PORTAGE_CONFIGROOT
	run_case portageq_repos portageq repos_config / location
	run_case portageq_profile readlink -f /etc/portage/make.profile
	run_case portageq_all_best_visible portageq all_best_visible /
	run_case portageq_vdb_path portageq vdb_path /
fi

if have equery; then
	run_case equery_installed equery -q list '*'
	run_case equery_hasuse equery -q hasuse '*'
fi
have euse && run_case euse_active euse --active
have eix-installed && run_case eix_installed_companion eix-installed
if have qlist; then run_case qlist_installed qlist -ICv; fi
if have qdepends; then run_case qdepends_installed qdepends -IC; fi
if have eix; then
	run_case eix_installed eix -I -c
	run_case eix_portage_ecosystem eix -c 'app-portage/*'
fi
have eix-test-obsolete && run_case eix_test_obsolete eix-test-obsolete
have genlop && run_case genlop_history genlop -g -n -l
have emlop && run_case emlop_history emlop log --utc --color=no --output=tab --last=200
have emlop && run_case emlop_stats emlop stats --utc --color=no --output=tab --show=a
have golop && run_case golop_history golop -e
have qlop && run_case qlop_history qlop -C -M -m
have glsa-check && run_case glsa_affected glsa-check -n -l affected
have glsa-check && run_case glsa_pretend glsa-check -n -p affected
have emaint && run_case emaint_world emaint --check world
have emaint && run_case emaint_merges emaint --check merges
have eclean && run_case eclean_distfiles eclean --nocolor --pretend distfiles
have eclean && run_case eclean_packages eclean --nocolor --pretend packages
have revdep-rebuild && run_case revdep_rebuild revdep-rebuild --pretend

read -r -a targets <<< "$targets_raw"
for target in "${targets[@]}"; do
	if [[ ! $target =~ ^[A-Za-z0-9+_.@/-]+$ ]]; then
		printf 'capture: refusing unsafe target: %s\n' "$target" >&2
		failures=$((failures + 1))
		continue
	fi
	safe=${target//\//_}
	safe=${safe//@/set_}
	if have emerge; then
		run_case "emerge_${safe}_shallow" emerge --pretend --verbose --color=n --columns=120 "$target"
		run_case "emerge_${safe}_deep" emerge --pretend --verbose --deep --newuse --with-bdeps=y --complete-graph --backtrack=1000 --color=n --columns=120 "$target"
	fi
	if have equery; then
		run_case "equery_${safe}_uses" equery -q uses "$target"
		run_case "equery_${safe}_depends" equery -q depends "$target"
		run_case "equery_${safe}_depgraph" equery -q depgraph "$target"
		run_case "equery_${safe}_meta" equery -q meta "$target"
	fi
	if have epkginfo; then run_case "epkginfo_${safe}" epkginfo "$target"; fi
	if have eshowkw; then run_case "eshowkw_${safe}" eshowkw "$target"; fi
	if have qatom; then run_case "qatom_${safe}" qatom "$target"; fi
	if have qdepends; then run_case "qdepends_${safe}" qdepends "$target"; fi
	if have qlist; then run_case "qlist_${safe}" qlist -ICv "$target"; fi
	if have qsearch; then run_case "qsearch_${safe}" qsearch -S "$target"; fi
	if have qcheck; then run_case "qcheck_${safe}" qcheck -C -q "$target"; fi
	if have qsize; then run_case "qsize_${safe}" qsize -C "$target"; fi
	if have qkeyword; then run_case "qkeyword_${safe}" qkeyword -C "$target"; fi
	if have quse; then run_case "quse_${safe}" quse -C "$target"; fi
	if have genlop; then run_case "genlop_${safe}" genlop -g -n -i "$target"; fi
	if have emlop; then run_case "emlop_${safe}" emlop log --utc --color=no --output=tab --exact --last=50 "$target"; fi
	if have golop; then run_case "golop_${safe}" golop -t "$target"; fi
	if have qlop; then run_case "qlop_${safe}" qlop -C -M -E "$target"; fi
	if have eix; then run_case "eix_${safe}" eix -e "$target"; fi
done

if have emerge; then
	run_case emerge_system_shallow emerge --pretend --verbose --color=n --columns=120 @system
	run_case emerge_system_deep emerge --pretend --verbose --deep --newuse --with-bdeps=y --complete-graph --backtrack=1000 --color=n --columns=120 @system
	if [[ $mode == full ]]; then
		run_case emerge_world_complete emerge --pretend --verbose --tree --verbose-conflicts --deep --newuse --with-bdeps=y --complete-graph --backtrack=1000 --color=n --columns=120 @world
		run_case emerge_info emerge --info
	fi
fi

if [[ $mode == privileged ]]; then
	run_case identity id
	if have portageq; then run_case privileged_portageq_env portageq envvar USE FEATURES ACCEPT_KEYWORDS ACCEPT_LICENSE ROOT SYSROOT BROOT; fi
	if have emerge; then run_case privileged_system emerge --pretend --verbose --color=n --columns=120 @system; fi
fi

printf 'cases=%d\nfailures=%d\nfinished_utc=%(%Y-%m-%dT%H:%M:%SZ)T\n' \
	"$case_index" "$failures" -1 >> "$output/capture.meta"
printf 'capture complete: %d cases, %d nonzero statuses\n' "$case_index" "$failures"
exit 0
