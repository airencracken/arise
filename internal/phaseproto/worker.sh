#!/usr/bin/env bash

exec 3>&1
readonly ___ARISE_WORKER_PID=$BASHPID
___ARISE_SEQUENCE_FILE=
___ARISE_LOG_FILE=
___arise_cleanup_worker_temporaries() {
	if [[ $BASHPID == "$___ARISE_WORKER_PID" && ${___ARISE_RESULT_EMITTED-} != 1 && -f $___ARISE_SEQUENCE_FILE ]]; then
		if [[ -f $___ARISE_LOG_FILE ]]; then
			___arise_emit '"kind":"log","stream":"stdout","message":"'"$(___arise_escape_json "$(<"$___ARISE_LOG_FILE")")"'"'
		fi
		___arise_emit '"kind":"result","exit_code":1'
	fi
  [[ -z $___ARISE_SEQUENCE_FILE ]] || rm -f -- "$___ARISE_SEQUENCE_FILE"
  [[ -z $___ARISE_LOG_FILE ]] || rm -f -- "$___ARISE_LOG_FILE"
  [[ -z ${___ARISE_NORMALIZED_SNAPSHOT-} ]] || rm -f -- "$___ARISE_NORMALIZED_SNAPSHOT"
}
trap ___arise_cleanup_worker_temporaries EXIT
___ARISE_SEQUENCE_FILE=$(mktemp) || {
  printf 'phase worker: cannot create sequence file\n' >&2
  exit 1
}
printf '0\n' > "$___ARISE_SEQUENCE_FILE"
declare -A ___ARISE_INHERITING=()
declare -A ___ARISE_INHERITED=()
declare -A ___ARISE_ECLASS_METADATA=()
___arise_escape_json() {
  local s=$1 out= ch escaped
  local i code
  for ((i=0; i<${#s}; i++)); do
    ch=${s:i:1}
    case $ch in
      '"') out+='\"' ;;
      \\) out+='\\' ;;
      $'\b') out+='\b' ;;
      $'\f') out+='\f' ;;
      $'\n') out+='\n' ;;
      $'\r') out+='\r' ;;
      $'\t') out+='\t' ;;
      *)
        printf -v code '%d' "'$ch"
        if (( code < 32 )); then
          printf -v escaped '\\u%04x' "$code"
          out+=$escaped
        else
          out+=$ch
        fi
        ;;
    esac
  done
  printf '%s' "$out"
}
___arise_emit() {
  local sequence
  sequence=$(<"$___ARISE_SEQUENCE_FILE")
  printf '{"protocol":1,"id":"%s","sequence":%d,%s}\n' "$___ARISE_ID" "$sequence" "$1" >&3
  printf '%d\n' "$((sequence+1))" > "$___ARISE_SEQUENCE_FILE"
}
die() { printf '%s\n' "${*:-die called}"; exit 1; }
___arise_elog() { local class=$1; shift; printf '\036ARISE_ELOG|%s|%s\n' "$class" "$*"; }
einfo() { ___arise_elog INFO "$@"; }
elog() { ___arise_elog LOG "$@"; }
ewarn() { ___arise_elog WARN "$@"; }
eerror() { ___arise_elog ERROR "$@"; }
eqawarn() { ___arise_elog QA "$@"; }
eqatag() {
	[[ ${1-} == -v ]] && shift
	local tag=${1-}; shift || :
	___arise_elog QA "${tag}${*:+: $*}"
}
contains_word() { has "$@"; }
find0() { find -files0-from - "$@"; }
___arise_add_sandbox_path() {
	local variable=${1-} path
	shift || return 2
	for path in "$@"; do
		[[ $path ]] || continue
		if [[ ${!variable-} ]]; then
			printf -v "$variable" '%s:%s' "${!variable}" "$path"
		else
			printf -v "$variable" '%s' "$path"
		fi
	done
	export "$variable"
}
addread() { ___arise_add_sandbox_path SANDBOX_READ "$@"; }
addwrite() { ___arise_add_sandbox_path SANDBOX_WRITE "$@"; }
adddeny() { ___arise_add_sandbox_path SANDBOX_DENY "$@"; }
addpredict() { ___arise_add_sandbox_path SANDBOX_PREDICT "$@"; }
___eapi_has_version_functions() { [[ ${1-${EAPI-0}} != [0-6] ]]; }
___eapi_has_strict_keepdir() { [[ ${1-${EAPI-0}} != [0-7] ]]; }
___makeopts_jobs() {
	local token jobs=1
	for token in ${MAKEOPTS-}; do
		[[ $token =~ ^-j([0-9]+)$ ]] && jobs=${BASH_REMATCH[1]}
	done
	printf '%s\n' "$jobs"
}
___parallel() (
	local max_procs retval=0 arg i=0 status
	max_procs=$(___makeopts_jobs)
	while IFS= read -r -d '' arg; do
		if (( i >= max_procs )); then
			wait -n; status=$?; (( i-- )); (( status == 0 )) || { retval=$status; break; }
		fi
		"$@" "$arg" & (( ++i ))
	done
	while (( i-- > 0 )); do wait -n || retval=$?; done
	return "$retval"
)
ebegin() { ___arise_elog INFO "$* ..."; }
eend() {
  local status=${1-} message=${2-}
  [[ $status =~ ^[0-9]+$ ]] || return 2
  if (( status == 0 )); then
    ___arise_elog INFO "${message:-ok}"
  else
    ___arise_elog ERROR "${message:-failed}"
  fi
  return "$status"
}
has() {
  local needle=${1-} candidate
  (( $# > 0 )) || return 1
  shift
  for candidate in "$@"; do [[ $candidate == "$needle" ]] && return 0; done
  return 1
}
debug-print() { :; }
debug-print-function() { :; }
use() {
  local flag=${1-} negate=0 enabled=1
	[[ $flag ]] || return 1
	[[ $flag == !* ]] && { negate=1; flag=${flag#!}; }
	[[ " ${USE-} " == *" $flag "* ]] || enabled=0
	(( negate )) && enabled=$((1-enabled))
	(( enabled ))
}
usex() {
  local flag=${1-} yes=${2-yes} no=${3-no}
  use "$flag" && printf '%s' "$yes" || printf '%s' "$no"
}
in_iuse() {
  local needle=${1-} flag
  [[ $needle ]] || return 1
  for flag in ${IUSE-}; do
    flag=${flag#+}; flag=${flag#-}
    [[ $flag == "$needle" ]] && return 0
  done
  return 1
}
usev() {
  local flag=${1-} value
  value=${2-$flag}
  use "$flag" || return 1
  printf '%s' "$value"
}
use_with() {
  local flag=${1-} name value=${3-}
  name=${2-$flag}
  [[ $flag && $name ]] || return 2
  if use "$flag"; then
    printf '%s' "--with-$name${value:+=$value}"
  else
    printf '%s' "--without-$name"
  fi
}
use_enable() {
  local flag=${1-} name value=${3-}
  name=${2-$flag}
  [[ $flag && $name ]] || return 2
  if use "$flag"; then
    printf '%s' "--enable-$name${value:+=$value}"
  else
    printf '%s' "--disable-$name"
  fi
}
nonfatal() {
  (( $# > 0 )) || { printf 'nonfatal requires a command\n'; return 1; }
  PORTAGE_NONFATAL=1 "$@"
}
has_version() {
  local domain=r
  while [[ ${1-} == -* ]]; do
    case ${1} in
      -b) domain=b ;;
      -d|-r) domain=r ;;
    esac
    shift
  done
  local query=${1-} line answer key
  [[ $query ]] || return 2
  key=${domain}$'\t'${query}
  while IFS= read -r line; do
    [[ ${line%$'\t'*} == "$key" ]] || continue
    [[ ${line##*$'\t'} == 1 ]]
    return
  done <<< "${___ARISE_HAS_VERSION-}"
  # Legacy caller-provided snapshots used an unqualified query key.
  while IFS= read -r line; do
    [[ ${line%%$'\t'*} == "$query" ]] || continue
    [[ ${line##*$'\t'} == 1 ]]
    return
  done <<< "${___ARISE_HAS_VERSION-}"
  if [[ ${___ARISE_QUERY_HELPER-} ]]; then
    answer=$("$___ARISE_QUERY_HELPER" __phase-query has-version "$domain" "$query" "${USE-}") || return 2
    case $answer in
      1) ___ARISE_HAS_VERSION+="${key}"$'\t1\n'; return 0 ;;
      0) ___ARISE_HAS_VERSION+="${key}"$'\t0\n'; return 1 ;;
      *) printf 'has_version helper returned invalid answer for %s\n' "$query" >&2; return 2 ;;
    esac
  fi
  printf 'has_version query was not preflighted: %s\n' "$query" >&2
  return 2
}
best_version() {
  local domain=r query line key answer
  while [[ ${1-} == -* ]]; do
    case ${1} in
      -b) domain=b ;;
      -d|-r) domain=r ;;
    esac
    shift
  done
  query=${1-}
  [[ $query ]] || return 2
  key=${domain}$'\t'${query}
  while IFS= read -r line; do
    [[ ${line%$'\t'*} == "$key" ]] || continue
    printf '%s\n' "${line##*$'\t'}"
    return 0
  done <<< "${___ARISE_BEST_VERSION-}"
  if [[ ${___ARISE_QUERY_HELPER-} ]]; then
    answer=$("$___ARISE_QUERY_HELPER" __phase-query best-version "$domain" "$query" "${USE-}") || return 2
    ___ARISE_BEST_VERSION+="${key}"$'\t'"${answer}"$'\n'
    printf '%s\n' "$answer"
    return 0
  fi
  return 0
}
__arise_ver_split() {
  local v=${1-} s c
  ___ARISE_VER_COMPONENTS=()
  while [[ $v ]]; do
    s=${v%%[a-zA-Z0-9]*}
    v=${v:${#s}}
    [[ $v == [0-9]* ]] && c=${v%%[^0-9]*} || c=${v%%[^a-zA-Z]*}
    v=${v:${#c}}
    ___ARISE_VER_COMPONENTS+=( "$s" "$c" )
  done
}
__arise_ver_range() {
  local range=${1-} max=${2-}
  [[ $range == [0-9]* ]] || return 2
  ___ARISE_VER_START=${range%-*}
  [[ $range == *-* ]] && ___ARISE_VER_END=${range#*-} || ___ARISE_VER_END=$___ARISE_VER_START
  [[ $___ARISE_VER_END ]] || ___ARISE_VER_END=$max
  [[ $___ARISE_VER_START -le $___ARISE_VER_END ]] || return 2
  (( ___ARISE_VER_END > max )) && ___ARISE_VER_END=$max
  return 0
}
ver_cut() {
  local range=${1-} v=${2-${PV-}} max start end IFS=
  [[ $range && $v ]] || return 2
  __arise_ver_split "$v"
  max=$((${#___ARISE_VER_COMPONENTS[@]}/2))
  __arise_ver_range "$range" "$max" || return
  start=$___ARISE_VER_START end=$___ARISE_VER_END
  (( start > 0 )) && start=$((start*2-1))
  printf '%s\n' "${___ARISE_VER_COMPONENTS[*]:start:end*2-start}"
}
ver_rs() {
  local v start end i max IFS=
  (( $# & 1 )) && v=${@: -1} || v=${PV-}
  [[ $v ]] || return 2
  __arise_ver_split "$v"
  max=$((${#___ARISE_VER_COMPONENTS[@]}/2-1))
  while (( $# >= 2 )); do
    __arise_ver_range "$1" "$max" || return
    start=$___ARISE_VER_START end=$___ARISE_VER_END
    for ((i=start*2; i<=end*2; i+=2)); do
      [[ $i -eq 0 && -z ${___ARISE_VER_COMPONENTS[i]} ]] && continue
      ___ARISE_VER_COMPONENTS[i]=$2
    done
    shift 2
  done
  printf '%s\n' "${___ARISE_VER_COMPONENTS[*]}"
}
ver_test() {
  local left=${1-} op=${2-} right=${3-}
  [[ $left && $op && $right ]] || return 2
  local first
  first=$(printf '%s\n%s\n' "$left" "$right" | sort -V | head -n1) || return
  case $op in
    -eq) [[ $left == "$right" ]] ;;
    -ne) [[ $left != "$right" ]] ;;
    -lt) [[ $left != "$right" && $first == "$left" ]] ;;
    -le) [[ $left == "$right" || $first == "$left" ]] ;;
    -gt) [[ $left != "$right" && $first == "$right" ]] ;;
    -ge) [[ $left == "$right" || $first == "$right" ]] ;;
    *) return 2 ;;
  esac
}
ver_replacing() {
  local op=${1-} right=${2-} replacing
  [[ $op && $right && $# -eq 2 ]] || return 2
  for replacing in ${REPLACING_VERSIONS-}; do
    ver_test "$replacing" "$op" "$right" && return 0
  done
  return 1
}
pipestatus() {
  local -a statuses=("${PIPESTATUS[@]}")
  local verbose=0 status
  [[ ${1-} != -v ]] || { verbose=1; shift; }
  (( $# == 0 )) || return 2
  (( verbose == 0 )) || printf '%s\n' "${statuses[*]}"
  for status in "${statuses[@]}"; do (( status == 0 )) || return "$status"; done
}
assert() {
  local -a statuses=("${PIPESTATUS[@]}")
  local status
  [[ ${EAPI-0} != 9 ]] || die "'assert' banned in EAPI ${EAPI}"
  for status in "${statuses[@]}"; do
    (( status == 0 )) || die "$@"
  done
}
get_libdir() {
  case ${ABI-} in
    amd64|x86_64) printf 'lib64' ;;
    x86) printf 'lib32' ;;
    *) [[ ${DEFAULT_ABI-} == amd64 ]] && printf 'lib64' || printf 'lib' ;;
  esac
}
___ARISE_DESTTREE=/usr
___ARISE_INSDESTTREE=/
___ARISE_EXEDESTTREE=/usr/bin
___ARISE_DOCDESTTREE=
declare -a ___ARISE_INSOPTIONS=(-m0644)
declare -a ___ARISE_EXEOPTIONS=(-m0755)
declare -a ___ARISE_DIROPTIONS=(-m0755)
declare -a ___ARISE_DOCOMPRESS=(/usr/share/doc /usr/share/info /usr/share/man)
declare -a ___ARISE_DOCOMPRESS_SKIP=()
declare -a ___ARISE_DOSTRIP=(/)
declare -a ___ARISE_DOSTRIP_SKIP=()
___arise_image_path() {
  local path=${1-} component
  local -a components=()
  [[ ${ED-} && $path == /* ]] || { printf 'image helper requires ED and an absolute destination\n'; return 125; }
	IFS=/ read -r -a components <<< "$path"
	for component in "${components[@]}"; do
		[[ $component != .. ]] || { printf 'image helper destination escapes image: %s\n' "$path"; return 125; }
	done
  printf '%s/%s' "${ED%/}" "${path#/}"
}
___arise_safe_name() { [[ ${1-} && $1 != */* && $1 != . && $1 != .. ]]; }
___arise_install_dir() { install -d "${___ARISE_DIROPTIONS[@]}" -- "$@"; }
into() { [[ ${1-} == /* ]] || { printf 'into requires an absolute prefix\n'; return 1; }; ___ARISE_DESTTREE=${1%/}; [[ $___ARISE_DESTTREE ]] || ___ARISE_DESTTREE=/; }
insinto() { [[ ${1-} == /* ]] || { printf 'insinto requires absolute image path\n'; return 1; }; ___ARISE_INSDESTTREE=$1; }
exeinto() { [[ ${1-} == /* ]] || { printf 'exeinto requires absolute image path\n'; return 1; }; ___ARISE_EXEDESTTREE=$1; }
insopts() { (( $# > 0 )) || { printf 'insopts requires options\n'; return 1; }; ___ARISE_INSOPTIONS=("$@"); }
exeopts() { (( $# > 0 )) || { printf 'exeopts requires options\n'; return 1; }; ___ARISE_EXEOPTIONS=("$@"); }
diropts() { (( $# > 0 )) || { printf 'diropts requires options\n'; return 1; }; ___ARISE_DIROPTIONS=("$@"); }
libopts() { printf 'libopts is banned for EAPI %s\n' "${EAPI-unknown}"; return 1; }
dohtml() { printf 'dohtml is banned for EAPI %s\n' "${EAPI-unknown}"; return 1; }
dosed() { printf 'dosed is banned for EAPI %s\n' "${EAPI-unknown}"; return 1; }
dolib() { printf 'dolib is banned for EAPI %s\n' "${EAPI-unknown}"; return 1; }
___arise_queue_paths() {
  local target=$1 mode=include path component
  shift
  [[ ${1-} == -x ]] && { mode=exclude; shift; }
  (( $# > 0 )) || { printf '%s requires paths\n' "$target"; return 1; }
  for path in "$@"; do
    [[ $path == /* ]] || path=/$path
    local -a components=(); IFS=/ read -r -a components <<< "$path"
    for component in "${components[@]}"; do [[ $component != .. ]] || { printf '%s path escapes image\n' "$target"; return 1; }; done
    path=${path%/}; [[ $path ]] || path=/
    case $target:$mode in
      docompress:include) ___ARISE_DOCOMPRESS+=("$path") ;;
      docompress:exclude) ___ARISE_DOCOMPRESS_SKIP+=("$path") ;;
      dostrip:include) ___ARISE_DOSTRIP+=("$path") ;;
      dostrip:exclude) ___ARISE_DOSTRIP_SKIP+=("$path") ;;
    esac
  done
}
docompress() { ___arise_queue_paths docompress "$@"; }
dostrip() { ___arise_queue_paths dostrip "$@"; }
docinto() {
  local path=${1-} component
  local -a components=()
	[[ $path ]] || { printf 'docinto requires a documentation path\n'; return 1; }
	[[ $path == / ]] && { ___ARISE_DOCDESTTREE=; return 0; }
	path=${path#/}
  IFS=/ read -r -a components <<< "$path"
  for component in "${components[@]}"; do [[ $component != .. ]] || { printf 'docinto path escapes documentation directory\n'; return 125; }; done
	___ARISE_DOCDESTTREE=$path
}
dobin() {
	[[ ${ED-} ]] || { printf 'dobin requires ED\n'; return 1; }
	(( $# > 0 )) || { printf 'dobin requires files\n'; return 1; }
	local source destination
	destination=$(___arise_image_path "$___ARISE_DESTTREE/bin") || return
	___arise_install_dir "$destination" || return
	for source in "$@"; do install "${___ARISE_EXEOPTIONS[@]}" -- "$source" "$destination/${source##*/}" || return; done
}
newbin() {
	[[ $# == 2 ]] || { printf 'newbin requires source and name\n'; return 1; }
	local old=$___ARISE_EXEDESTTREE
	___ARISE_EXEDESTTREE=$___ARISE_DESTTREE/bin
	newexe "$@"
	local status=$?
	___ARISE_EXEDESTTREE=$old
	return "$status"
}
dosbin() { local saved=$___ARISE_DESTTREE; into "$___ARISE_DESTTREE" || return; local old=$___ARISE_EXEDESTTREE; ___ARISE_EXEDESTTREE=$___ARISE_DESTTREE/sbin; doexe "$@"; local status=$?; ___ARISE_EXEDESTTREE=$old; ___ARISE_DESTTREE=$saved; return "$status"; }
newsbin() { [[ $# == 2 ]] || { printf 'newsbin requires source and name\n'; return 1; }; local old=$___ARISE_EXEDESTTREE; ___ARISE_EXEDESTTREE=$___ARISE_DESTTREE/sbin; newexe "$@"; local status=$?; ___ARISE_EXEDESTTREE=$old; return "$status"; }
doexe() {
  (( $# > 0 )) || { printf 'doexe requires files\n'; return 1; }
  local source destination; destination=$(___arise_image_path "$___ARISE_EXEDESTTREE") || return
  ___arise_install_dir "$destination" || return
  for source in "$@"; do install "${___ARISE_EXEOPTIONS[@]}" -- "$source" "$destination/${source##*/}" || return; done
}
newexe() {
  [[ $# == 2 ]] || { printf 'newexe requires source and name\n'; return 1; }
  ___arise_safe_name "$2" || { printf 'newexe requires a safe name\n'; return 1; }
  local destination source=$1 temporary=''; destination=$(___arise_image_path "$___ARISE_EXEDESTTREE") || return
  ___arise_install_dir "$destination" || return
  if [[ $source == - ]]; then
    temporary=$(mktemp "${T:-${TMPDIR:-/tmp}}/newexe-stdin.XXXXXXXX") || return
    cat > "$temporary" || { rm -f -- "$temporary"; return 1; }
    source=$temporary
  fi
  install "${___ARISE_EXEOPTIONS[@]}" -- "$source" "$destination/$2"
  local status=$?
  [[ -z $temporary ]] || rm -f -- "$temporary"
  return "$status"
}
___arise_source_eclass() {
	local ___arise_metadata_name ___arise_source_status
	local -a ___arise_metadata_keys=( IUSE REQUIRED_USE DEPEND BDEPEND RDEPEND PDEPEND IDEPEND )
	# PMS 10.2: eclass dependency keys accumulate; EAPI 8 adds these two.
	[[ $EAPI == 8 || $EAPI == 9 ]] && ___arise_metadata_keys+=( PROPERTIES RESTRICT )
	local -A ___arise_metadata_saved=()
	for ___arise_metadata_name in "${___arise_metadata_keys[@]}"; do
		[[ -v $___arise_metadata_name ]] && ___arise_metadata_saved[$___arise_metadata_name]=${!___arise_metadata_name}
		unset "$___arise_metadata_name"
	done
	source "$1"
	___arise_source_status=$?
	for ___arise_metadata_name in "${___arise_metadata_keys[@]}"; do
		if [[ ${!___arise_metadata_name-} ]]; then
			___ARISE_ECLASS_METADATA[$___arise_metadata_name]+=" ${!___arise_metadata_name}"
		fi
		if [[ -v ___arise_metadata_saved[$___arise_metadata_name] ]]; then
			printf -v "$___arise_metadata_name" '%s' "${___arise_metadata_saved[$___arise_metadata_name]}"
		else
			unset "$___arise_metadata_name"
		fi
	done
	return "$___arise_source_status"
}
inherit() {
  local ___arise_name ___arise_directory ___arise_path ___arise_old_eclass=${ECLASS-}
  for ___arise_name in "$@"; do
    [[ $___arise_name =~ ^[A-Za-z0-9+_.-]+$ ]] || die "unsafe eclass name $___arise_name"
    [[ ${___ARISE_INHERITED[$___arise_name]-} ]] && continue
    [[ ${___ARISE_INHERITING[$___arise_name]-} ]] && die "circular eclass inherit: $___arise_name"
    ___arise_path=
    while IFS= read -r ___arise_directory; do
      [[ -f $___arise_directory/$___arise_name.eclass ]] && { ___arise_path=$___arise_directory/$___arise_name.eclass; break; }
    done <<< "${___ARISE_ECLASS_DIRS-}"
    [[ $___arise_path ]] || die "inherited eclass $___arise_name not found"
    ___ARISE_INHERITING[$___arise_name]=1
    ECLASS=$___arise_name
    ___arise_source_eclass "$___arise_path" || die "could not source eclass $___arise_name"
    unset '___ARISE_INHERITING[$___arise_name]'
    ___ARISE_INHERITED[$___arise_name]=1
    ECLASS=$___arise_old_eclass
  done
}
EXPORT_FUNCTIONS() {
  local phase
  [[ ${ECLASS-} ]] || { printf 'EXPORT_FUNCTIONS outside eclass\n'; return 1; }
  for phase in "$@"; do
    [[ $phase =~ ^(pkg_|src_)[A-Za-z0-9_]+$ ]] || { printf 'unsafe exported phase %s\n' "$phase"; return 1; }
    eval "$phase() { ${ECLASS}_${phase} \"\$@\"; }"
  done
}
eapply_user() {
  local basename directory patch_file tagfile
  local -A patch_by=()
  [[ ${WORKDIR-} ]] || { printf 'eapply_user requires WORKDIR\n'; return 1; }
  T=${T:-$WORKDIR/.arise-tmp}
  mkdir -p "$T" || return
  tagfile=$T/.portage_user_patches_applied
  [[ -f $tagfile ]] && return
  : > "$tagfile" || return
  while IFS= read -r directory; do
    [[ $directory ]] || continue
    [[ -e $directory && ! -d $directory ]] && { printf 'user patch path is not a directory: %s\n' "$directory"; return 1; }
    for patch_file in "$directory"/*.patch "$directory"/*.diff; do
      [[ -e $patch_file || -L $patch_file ]] || continue
      basename=${patch_file##*/}
      if [[ -s $patch_file && -f $patch_file ]]; then
        patch_by[$basename]=$patch_file
      else
        unset 'patch_by[$basename]'
      fi
    done
  done <<< "${___ARISE_USER_PATCH_DIRS-}"
  (( ${#patch_by[@]} == 0 )) && return 0
  while IFS= read -r -d '' basename; do
    patch_file=${patch_by[$basename]}
    ( cd "${S:-$WORKDIR}" && patch -p1 -f --no-backup-if-mismatch < "$patch_file" ) || return
  done < <(printf '%s\0' "${!patch_by[@]}" | LC_ALL=C sort -z)
}
eapply() {
  [[ ${1-} == -- ]] && shift
  local patch_file candidate
  local -a patches=()
  for patch_file in "$@"; do
    if [[ -d $patch_file ]]; then
      patches=()
      while IFS= read -r -d '' candidate; do patches+=("$candidate"); done < <(find "$patch_file" -maxdepth 1 -type f \( -name '*.patch' -o -name '*.diff' \) -print0 | LC_ALL=C sort -z)
      (( ${#patches[@]} > 0 )) || { printf 'eapply: patch directory is empty: %s\n' "$patch_file"; return 1; }
      for candidate in "${patches[@]}"; do
        patch -p1 -f --no-backup-if-mismatch < "$candidate" || return
      done
      continue
    fi
    [[ -f $patch_file ]] || { printf 'eapply: patch not found: %s\n' "$patch_file"; return 1; }
    patch -p1 -f --no-backup-if-mismatch < "$patch_file" || return
  done
}
unpack() {
  local archive source basename output
  for archive in "$@"; do
    source=$archive
    [[ -e $source ]] || source=${DISTDIR-}/$archive
    [[ -f $source ]] || { printf 'unpack: archive not found: %s\n' "$archive"; return 1; }
    case $source in
      *.tar.gz|*.tgz) tar --no-same-owner -xzf "$source" || return ;;
      *.tar.bz2|*.tbz2) tar --no-same-owner -xjf "$source" || return ;;
      *.tar.xz|*.txz) tar --no-same-owner -xJf "$source" || return ;;
      *.tar.zst|*.tzst) tar --no-same-owner --zstd -xf "$source" || return ;;
      *.tar) tar --no-same-owner -xf "$source" || return ;;
      *.gz) basename=${archive##*/}; output=${basename%.gz}; gzip -dc "$source" > "$output" || return ;;
      *.bz2) basename=${archive##*/}; output=${basename%.bz2}; bzip2 -dc "$source" > "$output" || return ;;
      *.xz) basename=${archive##*/}; output=${basename%.xz}; xz -dc "$source" > "$output" || return ;;
      *.zip) unzip -qo "$source" || return ;;
      *.deb) ar p "$source" data.tar.xz | tar --no-same-owner -xJf - || ar p "$source" data.tar.zst | tar --no-same-owner --zstd -xf - || return ;;
      # PMS requires unpack to silently skip files with unsupported suffixes.
      # This is essential for default_src_unpack, since A also contains patch,
      # signature, and other non-archive distfiles used by later phases.
      *) continue ;;
    esac
  done
}
doins() {
	[[ ${ED-} ]] || { printf 'doins requires ED\n'; return 1; }
	local recursive=0 source destination; destination=$(___arise_image_path "$___ARISE_INSDESTTREE") || return
	[[ ${1-} == -r ]] && { recursive=1; shift; }
	(( $# > 0 )) || { printf 'doins requires files\n'; return 1; }
	___arise_install_dir "$destination" || return
	for source in "$@"; do
		if (( recursive )); then cp -R -- "$source" "$destination/" || return
		else install "${___ARISE_INSOPTIONS[@]}" -- "$source" "$destination/" || return; fi
	done
}
newins() {
  [[ $# == 2 ]] || { printf 'newins requires source and name\n'; return 1; }
  ___arise_safe_name "$2" || { printf 'newins requires a safe name\n'; return 1; }
  local destination source=$1 temporary=''; destination=$(___arise_image_path "$___ARISE_INSDESTTREE") || return
  ___arise_install_dir "$destination" || return
  if [[ $source == - ]]; then
    temporary=$(mktemp "${T:-${TMPDIR:-/tmp}}/newins-stdin.XXXXXXXX") || return
    cat > "$temporary" || { rm -f -- "$temporary"; return 1; }
    source=$temporary
  fi
  install "${___ARISE_INSOPTIONS[@]}" -- "$source" "$destination/$2"
  local status=$?
  [[ -z $temporary ]] || rm -f -- "$temporary"
  return "$status"
}
___arise_special_install() {
  local destination=$1 mode=$2 rename=${3-} source target temporary=''
  shift 3
  (( $# > 0 )) || die "installation helper requires files"
  target=$(___arise_image_path "$destination") || return
  install -d -m0755 -- "$target" || die "could not create installation directory $destination"
  if [[ $rename ]]; then
    [[ $# == 1 ]] || die "renaming helper requires one source"
    ___arise_safe_name "$rename" || die "renaming helper requires a safe name"
    source=$1
    if [[ $source == - ]]; then
      temporary=$(mktemp "${T:-${TMPDIR:-/tmp}}/special-install-stdin.XXXXXXXX") || die "could not stage helper standard input"
      cat > "$temporary" || { rm -f -- "$temporary"; die "could not read helper standard input"; }
      source=$temporary
    fi
    install -m "$mode" -- "$source" "$target/$rename"
    local status=$?
    [[ -z $temporary ]] || rm -f -- "$temporary"
    (( status == 0 )) || die "could not install $source as $destination/$rename"
    return 0
  fi
  for source in "$@"; do
    install -m "$mode" -- "$source" "$target/${source##*/}" ||
      die "could not install $source into $destination"
  done
}
doinitd() { ___arise_special_install /etc/init.d 0755 '' "$@"; }
newinitd() { [[ $# == 2 ]] || { printf 'newinitd requires source and name\n'; return 1; }; ___arise_special_install /etc/init.d 0755 "$2" "$1"; }
doconfd() { if [[ ${EAPI-} == 8 ]]; then ___arise_special_install /etc/conf.d 0644 '' "$@"; else local old=$___ARISE_INSDESTTREE; ___ARISE_INSDESTTREE=/etc/conf.d; doins "$@"; local status=$?; ___ARISE_INSDESTTREE=$old; return "$status"; fi; }
newconfd() { [[ $# == 2 ]] || { printf 'newconfd requires source and name\n'; return 1; }; if [[ ${EAPI-} == 8 ]]; then ___arise_special_install /etc/conf.d 0644 "$2" "$1"; else local old=$___ARISE_INSDESTTREE; ___ARISE_INSDESTTREE=/etc/conf.d; newins "$@"; local status=$?; ___ARISE_INSDESTTREE=$old; return "$status"; fi; }
doenvd() { if [[ ${EAPI-} == 8 ]]; then ___arise_special_install /etc/env.d 0644 '' "$@"; else local old=$___ARISE_INSDESTTREE; ___ARISE_INSDESTTREE=/etc/env.d; doins "$@"; local status=$?; ___ARISE_INSDESTTREE=$old; return "$status"; fi; }
newenvd() { [[ $# == 2 ]] || { printf 'newenvd requires source and name\n'; return 1; }; if [[ ${EAPI-} == 8 ]]; then ___arise_special_install /etc/env.d 0644 "$2" "$1"; else local old=$___ARISE_INSDESTTREE; ___ARISE_INSDESTTREE=/etc/env.d; newins "$@"; local status=$?; ___ARISE_INSDESTTREE=$old; return "$status"; fi; }
dodir() { local path destination; (( $# > 0 )) || { printf 'dodir requires paths\n'; return 1; }; for path in "$@"; do destination=$(___arise_image_path "$path") || return; install -d "${___ARISE_DIROPTIONS[@]}" -- "$destination" || return; done; }
keepdir() {
  local path destination keep=${CATEGORY:-category}_${PN:-package}_${SLOT:-0}
  keep=${keep//\//_}
  for path in "$@"; do dodir "$path" || return; destination=$(___arise_image_path "$path") || return; : > "$destination/.keep_$keep" || return; done
}
doheader() { local old=$___ARISE_INSDESTTREE oldopts=("${___ARISE_INSOPTIONS[@]}") olddiropts=("${___ARISE_DIROPTIONS[@]}"); ___ARISE_INSDESTTREE=/usr/include; if [[ ${EAPI-} == 8 ]]; then ___ARISE_INSOPTIONS=(-m0644); ___ARISE_DIROPTIONS=(-m0755); fi; doins "$@"; local status=$?; ___ARISE_INSDESTTREE=$old; ___ARISE_INSOPTIONS=("${oldopts[@]}"); ___ARISE_DIROPTIONS=("${olddiropts[@]}"); return "$status"; }
newheader() { [[ $# == 2 ]] || { printf 'newheader requires source and name\n'; return 1; }; local old=$___ARISE_INSDESTTREE oldopts=("${___ARISE_INSOPTIONS[@]}") olddiropts=("${___ARISE_DIROPTIONS[@]}"); ___ARISE_INSDESTTREE=/usr/include; if [[ ${EAPI-} == 8 ]]; then ___ARISE_INSOPTIONS=(-m0644); ___ARISE_DIROPTIONS=(-m0755); fi; newins "$@"; local status=$?; ___ARISE_INSDESTTREE=$old; ___ARISE_INSOPTIONS=("${oldopts[@]}"); ___ARISE_DIROPTIONS=("${olddiropts[@]}"); return "$status"; }
dolib.a() { local old=$___ARISE_INSDESTTREE oldopts=("${___ARISE_INSOPTIONS[@]}"); ___ARISE_INSDESTTREE=$___ARISE_DESTTREE/$(get_libdir); ___ARISE_INSOPTIONS=(-m0644); doins "$@"; local status=$?; ___ARISE_INSDESTTREE=$old; ___ARISE_INSOPTIONS=("${oldopts[@]}"); return "$status"; }
dolib.so() { local old=$___ARISE_INSDESTTREE oldopts=("${___ARISE_INSOPTIONS[@]}"); ___ARISE_INSDESTTREE=$___ARISE_DESTTREE/$(get_libdir); ___ARISE_INSOPTIONS=(-m0755); doins "$@"; local status=$?; ___ARISE_INSDESTTREE=$old; ___ARISE_INSOPTIONS=("${oldopts[@]}"); return "$status"; }
newlib.a() { [[ $# == 2 ]] || { printf 'newlib.a requires source and name\n'; return 1; }; local old=$___ARISE_INSDESTTREE oldopts=("${___ARISE_INSOPTIONS[@]}"); ___ARISE_INSDESTTREE=$___ARISE_DESTTREE/$(get_libdir); ___ARISE_INSOPTIONS=(-m0644); newins "$@"; local status=$?; ___ARISE_INSDESTTREE=$old; ___ARISE_INSOPTIONS=("${oldopts[@]}"); return "$status"; }
newlib.so() { [[ $# == 2 ]] || { printf 'newlib.so requires source and name\n'; return 1; }; local old=$___ARISE_INSDESTTREE oldopts=("${___ARISE_INSOPTIONS[@]}"); ___ARISE_INSDESTTREE=$___ARISE_DESTTREE/$(get_libdir); ___ARISE_INSOPTIONS=(-m0755); newins "$@"; local status=$?; ___ARISE_INSDESTTREE=$old; ___ARISE_INSOPTIONS=("${oldopts[@]}"); return "$status"; }
fperms() {
	[[ ${ED-} ]] || { printf 'fperms requires ED\n'; return 1; }
	local mode=${1-} path destination
	[[ $mode ]] || return 1
	shift
	for path in "$@"; do destination=$(___arise_image_path "$path") || return; chmod "$mode" "$destination" || return; done
}
dosym() {
	[[ ${ED-} ]] || { printf 'dosym requires ED\n'; return 1; }
	local relative= target destination image_destination
	if [[ ${1-} == -r ]]; then
		[[ ${EAPI-0} != [0-7] ]] || { printf 'dosym -r requires EAPI 8 or newer\n'; return 1; }
		relative=1
		shift
	fi
	[[ $# == 2 ]] || { printf 'dosym requires target and destination\n'; return 1; }
	target=$1 destination=$2
	[[ $target && $destination == /* ]] || { printf 'dosym requires target and absolute destination\n'; return 1; }
	if [[ $relative ]]; then
		[[ $target == /* ]] || { printf 'dosym -r requires an absolute target\n'; return 1; }
		local target_canonical linkdir comp slash i prev out IFS=/
		read -r -d '' -a target_canonical < <(printf '%s\0' "$target")
		while true; do
			prev=
			for i in "${!target_canonical[@]}"; do
				if [[ -z ${target_canonical[i]} || ${target_canonical[i]} == . ]]; then
					unset 'target_canonical[i]'
				elif [[ ${target_canonical[i]} != .. ]]; then
					prev=$i
				elif [[ $prev ]]; then
					unset 'target_canonical[prev]' 'target_canonical[i]'
					continue 2
				fi
			done
			break
		done
		target=/${target_canonical[*]}
		linkdir=${destination%/*}
		local old_ifs=${IFS-$' \t\n'}
		IFS=/
		for comp in ${linkdir}; do
			[[ $comp ]] || continue
			if [[ ${target#/} == "$comp"/* ]]; then
				target=/${target#/}; target=/${target#/$comp/}
			else
				target=../${target#/}
			fi
		done
		IFS=$old_ifs
		target=${target#/}
		[[ $target ]] || target=.
	fi
	image_destination=$(___arise_image_path "$destination") || return
	___arise_install_dir "${image_destination%/*}" || return
	ln -snf -- "$target" "$image_destination"
}
dohard() { printf 'dohard is banned for EAPI %s\n' "${EAPI-unknown}"; return 125; }
fowners() {
  [[ $# -ge 2 ]] || { printf 'fowners requires owner and paths\n'; return 1; }
  local owner=$1 path destination
  shift
  for path in "$@"; do destination=$(___arise_image_path "$path") || return; chown -- "$owner" "$destination" || return; done
}
emake() {
  local -a makeopts=()
  [[ ${MAKEOPTS-} ]] && read -r -a makeopts <<< "$MAKEOPTS"
  "${MAKE:-make}" "${makeopts[@]}" ${EXTRA_EMAKE-} "$@"
}
econf() {
  local source=${ECONF_SOURCE:-.} prefix=${EPREFIX-}/usr conf_prefix=${EPREFIX-}/usr build=${CBUILD:-${CHOST-}} argument
  local explicit_libdir=
  for argument in "$@"; do
    case $argument in
      --exec-prefix=*) conf_prefix=${argument#*=} ;;
      --prefix=*) conf_prefix=${argument#*=} ;;
      --libdir=*) explicit_libdir=1 ;;
    esac
  done
  local conf_help=
  local -a defaults=(
    "--prefix=$prefix"
    "--datadir=$prefix/share"
    "--mandir=$prefix/share/man"
    "--infodir=$prefix/share/info"
    "--sysconfdir=${EPREFIX-}/etc"
    "--localstatedir=${EPREFIX-}/var/lib"
  )
  [[ $explicit_libdir ]] || defaults+=("--libdir=$conf_prefix/$(get_libdir)")
  # Match the configure defaults supplied by Portage for the EAPIs Arise
  # supports.  These are conditional because not every configure script
  # accepts the standard Automake/GNU directory and policy switches.
  conf_help=$("$source/configure" --help 2>/dev/null) || :
  if [[ ${EAPI-0} != [0-3] && $conf_help == *--disable-dependency-tracking* ]]; then
    defaults+=(--disable-dependency-tracking)
  fi
  if [[ ${EAPI-0} != [0-4] && $conf_help == *--disable-silent-rules* ]]; then
    defaults+=(--disable-silent-rules)
  fi
  if [[ ${EAPI-0} != [0-5] ]]; then
    [[ $conf_help == *--docdir* ]] && defaults+=("--docdir=${EPREFIX-}/usr/share/doc/${PF:-package}")
    [[ $conf_help == *--htmldir* ]] && defaults+=("--htmldir=${EPREFIX-}/usr/share/doc/${PF:-package}/html")
  fi
  if [[ ${EAPI-0} != [0-6] && $conf_help == *--with-sysroot* ]]; then
    defaults+=("--with-sysroot=${ESYSROOT:-/}")
  fi
  if [[ ${EAPI-0} != [0-7] ]]; then
    [[ $conf_help == *--datarootdir* ]] && defaults+=("--datarootdir=${EPREFIX-}/usr/share")
    if [[ $conf_help == *--enable-shared* && $conf_help == *--enable-static* ]]; then
      defaults+=(--disable-static)
    fi
  fi
  [[ $build ]] && defaults+=("--build=$build")
  [[ ${CHOST-} ]] && defaults+=("--host=$CHOST")
  [[ ${CTARGET-} ]] && defaults+=("--target=$CTARGET")
  "$source/configure" "${defaults[@]}" "$@" && return 0
  local status=$?
  [[ ${PORTAGE_NONFATAL-} ]] && return "$status"
  die "econf failed with status $status"
}
dodoc() {
	[[ ${ED-} ]] || { printf 'dodoc requires ED\n'; return 1; }
	local recursive=0 destination=$ED/usr/share/doc/${PF:-package}${___ARISE_DOCDESTTREE:+/$___ARISE_DOCDESTTREE} file
	[[ ${1-} == -r ]] && { recursive=1; shift; }
	[[ ${1-} == -- ]] && shift
	(( $# > 0 )) || { printf 'dodoc requires files\n'; return 1; }
  mkdir -p "$destination" || return
  for file in "$@"; do
		if [[ -d $file && $recursive == 0 ]]; then
			printf 'dodoc requires -r for directories: %s\n' "$file"
			return 1
		fi
		cp -R -- "$file" "$destination/" || return
	done
}
newdoc() {
  [[ $# == 2 ]] || { printf 'newdoc requires source and name\n'; return 1; }
  ___arise_safe_name "$2" || { printf 'newdoc requires a safe name\n'; return 1; }
  local old=$___ARISE_INSDESTTREE oldopts=("${___ARISE_INSOPTIONS[@]}") olddiropts=("${___ARISE_DIROPTIONS[@]}"); ___ARISE_INSDESTTREE=/usr/share/doc/${PF:-package}${___ARISE_DOCDESTTREE:+/$___ARISE_DOCDESTTREE}; ___ARISE_INSOPTIONS=(-m0644); ___ARISE_DIROPTIONS=(-m0755); newins "$@"; local status=$?; ___ARISE_INSDESTTREE=$old; ___ARISE_INSOPTIONS=("${oldopts[@]}"); ___ARISE_DIROPTIONS=("${olddiropts[@]}"); return "$status"
}
doman() {
  local file section destination
  for file in "$@"; do
    section=${file##*.}; [[ $section =~ ^[0-9][A-Za-z]*$ ]] || { printf 'doman cannot determine section for %s\n' "$file"; return 1; }
    destination=$(___arise_image_path "/usr/share/man/man${section:0:1}") || return
    mkdir -p "$destination" || return
    install -m0644 -- "$file" "$destination/${file##*/}" || return
  done
}
newman() {
  [[ $# == 2 ]] || { printf 'newman requires source and name\n'; return 1; }
  ___arise_safe_name "$2" || { printf 'newman requires a safe name\n'; return 1; }
  local section=${2##*.} destination
  [[ $section =~ ^[0-9][A-Za-z]*$ ]] || { printf 'newman cannot determine section for %s\n' "$2"; return 1; }
  destination=$(___arise_image_path "/usr/share/man/man${section:0:1}") || return
  mkdir -p "$destination" || return
  install -m0644 -- "$1" "$destination/$2"
}
doinfo() { ___arise_special_install /usr/share/info 0644 '' "$@"; }
domo() {
	[[ ${EAPI-} != 9 ]] || { printf 'domo is banned in EAPI 9\n'; return 1; }
  (( $# > 0 )) || { printf 'domo requires files\n'; return 1; }
  local source locale destination
  for source in "$@"; do
    [[ -f $source ]] || { printf 'domo source not found: %s\n' "$source"; return 1; }
    locale=${source##*/}; locale=${locale%.*}
    destination=$(___arise_image_path "/usr/share/locale/$locale/LC_MESSAGES") || return
    install -d -m0755 -- "$destination" || return
    install -m0644 -- "$source" "$destination/${MOPREFIX:-${PN:-messages}}.mo" || return
  done
}
einstalldocs() {
  local -a docs=()
  if declare -p DOCS >/dev/null 2>&1; then
    if [[ $(declare -p DOCS) == "declare -a"* ]]; then docs=("${DOCS[@]}"); else read -r -a docs <<< "$DOCS"; fi
  else
    local candidate
    for candidate in README* ChangeLog AUTHORS NEWS TODO CHANGES THANKS BUGS FAQ CREDITS CHANGELOG; do [[ -s $candidate ]] && docs+=("$candidate"); done
  fi
  (( ${#docs[@]} == 0 )) || dodoc "${docs[@]}"
}
default_src_prepare() {
  if declare -p PATCHES >/dev/null 2>&1; then
    if [[ $(declare -p PATCHES) == "declare -a"* ]]; then
      (( ${#PATCHES[@]} == 0 )) || eapply -- "${PATCHES[@]}" || return
    elif [[ ${PATCHES-} ]]; then eapply -- $PATCHES || return; fi
  fi
  eapply_user
}
default_src_unpack() { [[ -z ${A-} ]] || unpack $A; }
default_src_configure() { [[ ! -x ${ECONF_SOURCE:-.}/configure ]] || econf; }
default_src_compile() { [[ ! -f Makefile && ! -f GNUmakefile && ! -f makefile ]] || emake; }
default_src_test() {
  if emake check -n >/dev/null 2>&1; then emake check
  elif emake test -n >/dev/null 2>&1; then emake test
  fi
}
default_src_install() {
  if [[ -f Makefile || -f GNUmakefile || -f makefile ]]; then
    [[ ${D-} ]] || { printf 'default src_install requires D\n'; return 1; }
    emake DESTDIR="$D" install || return
  fi
  einstalldocs
}
default_pkg_setup() { :; }
default_pkg_pretend() { :; }
default_pkg_preinst() { :; }
default_pkg_postinst() { :; }
default_pkg_prerm() { :; }
default_pkg_postrm() { :; }
default_pkg_nofetch() { :; }
default() {
  local implementation=default_${EBUILD_PHASE_FUNC-}
  declare -F "$implementation" >/dev/null || { printf 'default phase unavailable: %s\n' "${EBUILD_PHASE-}"; return 1; }
  "$implementation" "$@"
}
___arise_finalize_install_image() {
  [[ ${ED-} ]] || return 0
  local relative path queued skip excluded file_kind compressor=${PORTAGE_COMPRESS:-gzip} suffix
  local -a compress_flags=()
	# Portage regenerates the canonical GNU Info directory index on the live
	# system. It must never escape the build image or become package-owned.
	if [[ -d ${ED%/}/usr/share/info ]]; then
		rm -f "${ED%/}"/usr/share/info/dir{,.info}{,.Z,.gz,.bz2,.lzma,.lz,.xz,.zst} || return
	fi
  [[ ${PORTAGE_COMPRESS_FLAGS-} ]] && read -r -a compress_flags <<< "$PORTAGE_COMPRESS_FLAGS"
	case $compressor in gzip) suffix=.gz ;; bzip2) suffix=.bz2 ;; xz) suffix=.xz ;; zstd) suffix=.zst ;; *) printf 'unsupported PORTAGE_COMPRESS: %s\n' "$compressor"; return 1 ;; esac
  for queued in "${___ARISE_DOCOMPRESS[@]}"; do
    path=$(___arise_image_path "$queued") || return
    [[ -e $path ]] || continue
    while IFS= read -r -d '' path; do
      relative=/${path#"${ED%/}"/}; excluded=0
      for skip in "${___ARISE_DOCOMPRESS_SKIP[@]}" "/usr/share/doc/${PF:-package}/html"; do
        [[ $relative == "$skip" || $relative == "$skip"/* ]] && { excluded=1; break; }
      done
      (( excluded )) && continue
		[[ $path == *.gz || $path == *.bz2 || $path == *.xz || $path == *.zst ]] && continue
		case $compressor in
		  gzip) gzip -9nf "${compress_flags[@]}" -- "$path" || return ;;
		  bzip2) bzip2 -9f "${compress_flags[@]}" -- "$path" || return ;;
		  xz) xz -9f "${compress_flags[@]}" -- "$path" || return ;;
		  zstd) zstd -q -19 -f --rm "${compress_flags[@]}" -- "$path" || return ;;
		esac
    done < <(find "$path" -type f -size +128c -print0)
  done
  if [[ ${___ARISE_STRIP-0} == 1 ]]; then
    for queued in "${___ARISE_DOSTRIP[@]}"; do
      path=$(___arise_image_path "$queued") || return
      [[ -e $path ]] || continue
      while IFS= read -r -d '' path; do
        relative=/${path#"${ED%/}"/}; excluded=0
        for skip in "${___ARISE_DOSTRIP_SKIP[@]}"; do [[ $relative == "$skip" || $relative == "$skip"/* ]] && { excluded=1; break; }; done
        (( excluded )) && continue
		# readelf also accepts archives, including thin archives whose members
		# live outside the image. Match Portage's estrip object classification:
		# leave thin archives alone and retain regular archives' link symbols.
		file_kind=$(LC_ALL=C file -b -- "$path") || return
		case $file_kind in
		  *"current ar archive"*)
		    strip -g -- "$path" || return
		    ranlib -- "$path" || return ;;
		  *"SB executable"*|*"SB pie executable"*|*"SB shared object"*|*"SB relocatable"*)
		    strip --strip-unneeded -- "$path" || return ;;
		esac
      done < <(find "$path" -type f -print0)
    done
  fi
	if [[ ${EAPI-} == 8 ]]; then
		find "$ED" -depth -mindepth 1 -type d -empty -delete || return
	fi
	return 0
}
___arise_run_install_qa_checks() (
	local check
	: "${PORTAGE_INST_UID:=0}" "${PORTAGE_INST_GID:=0}"
	: "${PORTAGE_TMPDIR:=${WORKDIR%/*}}" "${PORTAGE_PYTHON:=python3}"
	# Install QA scripts use these Portage logging helpers. Define them only
	# inside the QA subshell so they cannot enter the package environment.
	__quiet_mode() { [[ ${PORTAGE_QUIET:-0} -eq 1 ]]; }
	__vecho() { __quiet_mode || echo "$@" >&2; }
	while IFS= read -r check; do
		[[ $check ]] || continue
		# Portage treats each QA check as an external script while passing the
		# package environment through. Keep its image edits and diagnostics,
		# but discard functions, options, variables, traps, and directory changes.
		if ! ( source "$check" ); then
			eerror "Install QA check ${check##*/} failed to run"
		fi
	done <<< "${___ARISE_INSTALL_QA_CHECKS-}"
	return 0
)
___arise_persist_variable() {
	[[ ! ${___ARISE_BASH_VARIABLES[$1]-} ]] || return 1
	case $1 in
		S|___ARISE_INHERITED|___ARISE_DOCOMPRESS|___ARISE_DOCOMPRESS_SKIP|___ARISE_DOSTRIP|___ARISE_DOSTRIP_SKIP) return 0 ;;
		___ARISE_DESTTREE|___ARISE_INSDESTTREE|___ARISE_EXEDESTTREE|___ARISE_DOCDESTTREE|___ARISE_INSOPTIONS|___ARISE_EXEOPTIONS|___ARISE_DIROPTIONS) return 0 ;;
		___ARISE_*|SANDBOX_*|LD_PRELOAD|BASHPID|EUID|UID|PPID|SHELLOPTS|FUNCNAME|GROUPS|PIPESTATUS|DIRSTACK|LINENO|RANDOM|SECONDS|SHLVL|_|PWD|OLDPWD) return 1 ;;
		BASH_COMPAT|BASH_ENV|BASH_REMATCH|BASH_TRAPSIG|BASH_XTRACEFD|COMP_CWORD|COMP_POINT|COMP_WORDS|CHILD_MAX|COMPREPLY|COMP_LINE|COMP_TYPE|COMP_KEY|COLUMNS|CDPATH|COPROC|EXECIGNORE|ENV|FUNCNEST|FIGNORE|FCEDIT|GLOBIGNORE|GLOBSORT|HISTTIMEFORMAT|HISTFILESIZE|HISTCONTROL|HISTIGNORE|HISTFILE|HISTSIZE|HOSTFILE|INSIDE_EMACS|IGNOREEOF|INPUTRC|LINES|MAILCHECK|MAILPATH|MAPFILE|MAIL|OPTARG|POSIXLY_CORRECT|PROMPT_COMMAND|PROMPT_DIRTRIM|PS0|PS1|PS2|PS3|READLINE_ARGUMENT|READLINE_POINT|READLINE_LINE|READLINE_MARK|REPLY|TIMEFORMAT|TMOUT|auto_resume|histchars) return 1 ;;
		# Portage excludes ambient session/tool configuration when saving state.
		BLOCK_SIZE|COLORTERM|DF_BLOCK_SIZE|DU_BLOCK_SIZE|LS_BLOCK_SIZE|LS_COLORS|QUOTING_STYLE|TABSIZE|TERM|TIME_STYLE|DISPLAY|EDITOR|LESS|LESSOPEN|LOGNAME|PAGER|TERMCAP|USER|ftp_proxy|http_proxy|https_proxy|no_proxy|SSH_*|XDG_CONFIG_*|XDG_CURRENT_*|XDG_DATA_*|XDG_MENU_*|XDG_RUNTIME_*|XDG_SEAT_*|XDG_SESSION_*|XDG_VTNR|CVS_RSH|ECHANGELOG_USER|GPG_AGENT_INFO|SHELL_SETS_TITLE|STY|WINDOW|XAUTHORITY|CCACHE_*|DISTCC_*) return 1 ;;
		ACCEPT_LICENSE|BUILD_PREFIX|DOC_SYMLINKS_DIR|EBUILD_FORCE_TEST|FAKEROOTKEY|NOCOLOR|NO_COLOR|PKGDIR|PKGUSE|PKG_LOGDIR|PKG_TMPDIR|PORTAGE_BASHRC_FILES|PORTAGE_COMPRESS|PORTAGE_COMPRESS_EXCLUDE_SUFFIXES|PORTAGE_DOHTML_UNWARNED_SKIPPED_EXTENSIONS|PORTAGE_DOHTML_UNWARNED_SKIPPED_FILES|PORTAGE_DOHTML_WARN_ON_SKIPPED_FILES|PORTAGE_QUIET|PORTAGE_SANDBOX_DENY|PORTAGE_SANDBOX_PREDICT|PORTAGE_SANDBOX_READ|PORTAGE_SANDBOX_WRITE|PORTAGE_SOCKS5_PROXY|PREROOTPATH|ROOTPATH|RPMDIR|USE_EXPAND|COLS|EBUILD_MASTER_PID|ECLASS_DEPTH|ENDCOL|HOME|LAST_E_CMD|LAST_E_LEN|MISC_FUNCTIONS_ARGS|MOPREFIX|PORTAGE_BASHRCS_SOURCED|PORTAGE_COLOR_*|PORTAGE_NONFATAL|QA_INTERCEPTORS|RC_ENDCOL|XARGS|_RC_GET_KV_CACHE|INSTALL_MASK|PKG_INSTALL_MASK) return 1 ;;
		CATEGORY|PN|PV|PR|P|PVR|PF|SLOT|ROOT|EROOT|SYSROOT|ESYSROOT|BROOT|WORKDIR|D|ED|T|FILESDIR|DISTDIR|HOME|TMPDIR|TMP|TEMP|EBUILD_PHASE|EBUILD_PHASE_FUNC|PORTAGE_LOG_FILE|PORTAGE_BUILDDIR|PORTAGE_CONFIGROOT) return 1 ;;
	esac
	[[ $1 =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]
}
___arise_write_declaration() {
	local ___ARISE_DECLARATION_FLAGS=${1#declare -}
	___ARISE_DECLARATION_FLAGS=${___ARISE_DECLARATION_FLAGS%% *}
	# Portage retains package values but removes readonly when restoring state.
	___ARISE_DECLARATION_FLAGS=${___ARISE_DECLARATION_FLAGS//r/}
	printf 'declare -%s %s\n' "${___ARISE_DECLARATION_FLAGS:--}" "${1#declare -* }"
}
___arise_refresh_defined_phases() {
  DEFINED_PHASES=
  for ___ARISE_METADATA_PHASE in pkg_pretend pkg_setup src_unpack src_prepare src_configure src_compile src_test src_install pkg_preinst pkg_postinst pkg_prerm pkg_postrm pkg_config pkg_info pkg_nofetch; do
    declare -F "$___ARISE_METADATA_PHASE" >/dev/null && DEFINED_PHASES+="${DEFINED_PHASES:+ }${___ARISE_METADATA_PHASE}"
  done
  return 0
}
___arise_write_phase_environment() {
	local ___ARISE_SAVE_NAME ___ARISE_SAVE_DECLARATION
	local -a ___ARISE_SAVE_FUNCTIONS=()
	___arise_refresh_defined_phases
	printf '# ARISE_PHASE_STATE_V3\n' || return
	printf '# ARISE_LIFECYCLE_PHASES' || return
	for ___ARISE_SAVE_NAME in $DEFINED_PHASES; do
		[[ $___ARISE_SAVE_NAME != pkg_* ]] || printf ' %s' "$___ARISE_SAVE_NAME" || return
	done
	printf '\n' || return
	for ___ARISE_SAVE_NAME in "${!___ARISE_INITIAL_VARIABLES[@]}"; do
		___arise_persist_variable "$___ARISE_SAVE_NAME" || continue
		declare -p "$___ARISE_SAVE_NAME" >/dev/null 2>&1 || printf 'unset -v %s\n' "$___ARISE_SAVE_NAME" || return
	done
	# Bash quotes newlines inside declarations, so each complete variable is
	# emitted on one line. Dump in bulk to avoid a subshell per variable.
	while IFS= read -r ___ARISE_SAVE_DECLARATION; do
		___ARISE_SAVE_NAME=${___ARISE_SAVE_DECLARATION#declare }
		___ARISE_SAVE_NAME=${___ARISE_SAVE_NAME#* }
		___ARISE_SAVE_NAME=${___ARISE_SAVE_NAME%%=*}
		___arise_persist_variable "$___ARISE_SAVE_NAME" || continue
		___arise_write_declaration "$___ARISE_SAVE_DECLARATION" || return
	done < <(declare -p | /usr/bin/env LC_ALL=C PATH=/usr/bin:/bin sort)
	while IFS= read -r ___ARISE_SAVE_NAME; do
		case $___ARISE_SAVE_NAME in ___arise_*|___arise_run_one_phase) continue ;; esac
		[[ ! ${___ARISE_WORKER_FUNCTIONS[$___ARISE_SAVE_NAME]-} ]] || continue
		___ARISE_SAVE_FUNCTIONS+=("$___ARISE_SAVE_NAME")
	done < <(compgen -A function | /usr/bin/env LC_ALL=C PATH=/usr/bin:/bin sort -u)
	(( ${#___ARISE_SAVE_FUNCTIONS[@]} == 0 )) || declare -f "${___ARISE_SAVE_FUNCTIONS[@]}" || return
	if (( ${#___ARISE_SAVE_FUNCTIONS[@]} )); then
		# Read attributes separately so declaration-like text inside a function's
		# here-documents is never rewritten. Plain -f would print its body again.
		while IFS= read -r ___ARISE_SAVE_DECLARATION; do
			[[ $___ARISE_SAVE_DECLARATION == 'declare -f '* || $___ARISE_SAVE_DECLARATION == 'declare -fr '* ]] && continue
			___arise_write_declaration "$___ARISE_SAVE_DECLARATION" || return
		done < <(shopt -u extdebug; declare -Fp "${___ARISE_SAVE_FUNCTIONS[@]}")
	fi
	return 0
}
___arise_load_legacy_helpers() {
	local ___ARISE_LEGACY_LINE ___ARISE_LEGACY_NAME ___ARISE_LEGACY_DECLARATION
	[[ $___ARISE_SNAPSHOT_INPUT ]] || return 0
	while IFS= read -r ___ARISE_LEGACY_LINE; do
		[[ $___ARISE_LEGACY_LINE != '# ARISE_PHASE_STATE_V3' ]] || return 0
		[[ $___ARISE_LEGACY_LINE == '# ARISE_PHASE_STATE_V2' ]] || continue
		# V2 helper state used unreserved names. Import only these known fields.
		for ___ARISE_LEGACY_NAME in ARISE_INHERITED ARISE_DOCOMPRESS ARISE_DOCOMPRESS_SKIP ARISE_DOSTRIP ARISE_DOSTRIP_SKIP DESTTREE INSDESTTREE EXEDESTTREE DOCDESTTREE INSOPTIONS EXEOPTIONS DIROPTIONS; do
			___ARISE_LEGACY_DECLARATION=$(declare -p "$___ARISE_LEGACY_NAME" 2>/dev/null) || continue
			___ARISE_LEGACY_DECLARATION=${___ARISE_LEGACY_DECLARATION/ ${___ARISE_LEGACY_NAME}=/ ___ARISE_${___ARISE_LEGACY_NAME#ARISE_}=}
			builtin eval "${___ARISE_LEGACY_DECLARATION/declare /declare -g }" || return
			unset -v "$___ARISE_LEGACY_NAME" || return
		done
		return 0
	done < "$___ARISE_SNAPSHOT_INPUT"
	return 0
}
___arise_source_package_state() {
	# Select the source path here, then source it at global scope below.
	# declare statements sourced inside a function would otherwise be local.
	local ___ARISE_MARKER_LINE ___ARISE_NORMALIZE_STATUS
	[[ $___ARISE_SNAPSHOT_INPUT ]] || return 0
	while IFS= read -r ___ARISE_MARKER_LINE; do
		if [[ $___ARISE_MARKER_LINE == '# ARISE_PHASE_STATE_V3' ]]; then
			IFS= read -r ___ARISE_MARKER_LINE || return 2
			[[ $___ARISE_MARKER_LINE == '# ARISE_LIFECYCLE_PHASES' || $___ARISE_MARKER_LINE == '# ARISE_LIFECYCLE_PHASES '* ]] || {
				printf 'phase environment: missing lifecycle metadata\n' >&2
				return 2
			}
			return 0
		fi
		[[ $___ARISE_MARKER_LINE == '# ARISE_PHASE_STATE_V2' ]] || continue
		___ARISE_NORMALIZED_SNAPSHOT=$(mktemp "${___ARISE_SAVED_T:-/tmp}/.arise-state.XXXXXXXX") || return
		# Bash parses legacy values and functions in a child shell. Emitting
		# V3 there removes readonly without rewriting quoted data or function
		# here-documents as text, and without changing the original snapshot.
		if (
			source "$___ARISE_SNAPSHOT_INPUT" >>"$___ARISE_LOG_FILE" 2>&1 || exit $?
			___arise_load_legacy_helpers || exit $?
			___arise_write_phase_environment
		) > "$___ARISE_NORMALIZED_SNAPSHOT"; then
			return 0
		else
			___ARISE_NORMALIZE_STATUS=$?
		fi
		printf 'phase worker: legacy environment normalization failed: %s\n' "$___ARISE_SNAPSHOT_INPUT" >&2
		rm -f -- "$___ARISE_NORMALIZED_SNAPSHOT"
		___ARISE_NORMALIZED_SNAPSHOT=
		return "$___ARISE_NORMALIZE_STATUS"
	done < "$___ARISE_SNAPSHOT_INPUT"
	return 0
}
___arise_validate_source_directory() {
	local ___ARISE_RESOLVED_SOURCE ___ARISE_RESOLVED_WORK
	[[ ${S-} ]] || S=$___ARISE_SAVED_S
	if [[ ${S-} == "$___ARISE_SAVED_S" ]]; then
		# An explicit request can bind S separately from WORKDIR. A default
		# source below WORKDIR still needs checking after unpack replaces it.
		if [[ ! ${___ARISE_SAVED_WORKDIR-} || ! ${S-} || ( $S != "$___ARISE_SAVED_WORKDIR" && $S != "$___ARISE_SAVED_WORKDIR"/* ) ]]; then
			return 0
		fi
	fi
	if [[ ${S-} != /* || ! ${___ARISE_SAVED_WORKDIR-} ]]; then
		printf 'ebuild S escapes WORKDIR: %s\n' "${S-}"
		return 126
	fi
	___ARISE_RESOLVED_SOURCE=$(realpath -m -- "$S") || return
	___ARISE_RESOLVED_WORK=$(realpath -m -- "$___ARISE_SAVED_WORKDIR") || return
	if [[ $___ARISE_RESOLVED_SOURCE != "$___ARISE_RESOLVED_WORK" && $___ARISE_RESOLVED_SOURCE != "$___ARISE_RESOLVED_WORK"/* ]]; then
		printf 'ebuild S escapes WORKDIR: %s\n' "$S"
		return 126
	fi
}
declare -A ___ARISE_INITIAL_VARIABLES=() ___ARISE_WORKER_FUNCTIONS=() ___ARISE_BASH_VARIABLES=()
# Discover the running Bash version's own variables in a clean interpreter.
# Prefix guessing would discard application variables such as BASH_COMPLETION_*.
___ARISE_CLEAN_BASH_VARIABLES=$(env -i "$BASH" --noprofile --norc -c 'compgen -v') || {
	printf 'phase worker: cannot identify Bash runtime variables\n' >&2
	exit 1
}
while IFS= read -r ___ARISE_NAME; do
	[[ $___ARISE_NAME == PATH || $___ARISE_NAME == SHELL ]] || ___ARISE_BASH_VARIABLES[$___ARISE_NAME]=1
done <<< "$___ARISE_CLEAN_BASH_VARIABLES"
unset ___ARISE_CLEAN_BASH_VARIABLES
while IFS= read -r ___ARISE_NAME; do
	___arise_persist_variable "$___ARISE_NAME" && ___ARISE_INITIAL_VARIABLES["$___ARISE_NAME"]=1
done < <(compgen -v)
while IFS= read -r ___ARISE_NAME; do
	___ARISE_WORKER_FUNCTIONS["$___ARISE_NAME"]=1
done < <(compgen -A function)
___ARISE_LOG_FILE=$(mktemp) || {
  printf 'phase worker: cannot create log file\n' >&2
  exit 1
}
___ARISE_STATUS=0
___ARISE_DECLARED_S=
readonly ___ARISE_SNAPSHOT_INPUT=${___ARISE_ENVIRONMENT_OVERLAY:-${___ARISE_ENVIRONMENT:-}}
readonly ___ARISE_SNAPSHOT_EXTGLOB=$(shopt -p extglob)
readonly ___ARISE_SAVED_CATEGORY=${CATEGORY-} ___ARISE_SAVED_P=${P-} ___ARISE_SAVED_PF=${PF-} ___ARISE_SAVED_PN=${PN-}
readonly ___ARISE_SAVED_PV=${PV-} ___ARISE_SAVED_PR=${PR-} ___ARISE_SAVED_PVR=${PVR-} ___ARISE_SAVED_SLOT=${SLOT-}
readonly ___ARISE_SAVED_ROOT=${ROOT-} ___ARISE_SAVED_EROOT=${EROOT-} ___ARISE_SAVED_SYSROOT=${SYSROOT-} ___ARISE_SAVED_ESYSROOT=${ESYSROOT-} ___ARISE_SAVED_BROOT=${BROOT-}
readonly ___ARISE_SAVED_WORKDIR=${WORKDIR-} ___ARISE_SAVED_S=${S-} ___ARISE_SAVED_D=${D-} ___ARISE_SAVED_ED=${ED-} ___ARISE_SAVED_T=${T-}
readonly ___ARISE_SAVED_FILESDIR=${FILESDIR-} ___ARISE_SAVED_DISTDIR=${DISTDIR-} ___ARISE_SAVED_USE=${USE-}
readonly ___ARISE_SAVED_HOME=${HOME-} ___ARISE_SAVED_TMPDIR=${TMPDIR-} ___ARISE_SAVED_TMP=${TMP-} ___ARISE_SAVED_TEMP=${TEMP-}
readonly ___ARISE_SAVED_PATH=$PATH
declare -a ___ARISE_RUNTIME_VARIABLES=( FEATURES PORTAGE_FEATURES LC_MESSAGES LC_MONETARY LC_COLLATE LC_NUMERIC LC_CTYPE LC_PAPER LC_TIME LC_ALL LANG )
declare -A ___ARISE_SAVED_RUNTIME_SETTINGS=()
for ___ARISE_NAME in "${___ARISE_RUNTIME_VARIABLES[@]}"; do
	[[ ! -v $___ARISE_NAME ]] || ___ARISE_SAVED_RUNTIME_SETTINGS[$___ARISE_NAME]=${!___ARISE_NAME}
done
readonly ___ARISE_SAVED_BUILDDIR=${PORTAGE_BUILDDIR-} ___ARISE_SAVED_CONFIGROOT=${PORTAGE_CONFIGROOT-} ___ARISE_SAVED_LOG_FILE=${PORTAGE_LOG_FILE-}
declare -A ___ARISE_SAVED_SANDBOX_VALUES=() ___ARISE_SAVED_SANDBOX_EXPORTED=()
while IFS= read -r ___ARISE_NAME; do
  [[ $___ARISE_NAME == SANDBOX_* || $___ARISE_NAME == LD_PRELOAD ]] || continue
  ___ARISE_SAVED_SANDBOX_VALUES["$___ARISE_NAME"]=${!___ARISE_NAME-}
  [[ $(export -p 2>/dev/null) == *"declare -x $___ARISE_NAME="* || $(export -p 2>/dev/null) == *"declare -x $___ARISE_NAME" ]] &&
    ___ARISE_SAVED_SANDBOX_EXPORTED["$___ARISE_NAME"]=1
done < <(compgen -v)
___arise_restore_sandbox_environment() {
  local ___ARISE_CONTROL_NAME ___ARISE_CONTROL_DECLARATION ___ARISE_CONTROL_FLAGS
  while IFS= read -r ___ARISE_CONTROL_NAME; do
    [[ $___ARISE_CONTROL_NAME == SANDBOX_* || $___ARISE_CONTROL_NAME == LD_PRELOAD ]] || continue
	___ARISE_CONTROL_DECLARATION=$(declare -p "$___ARISE_CONTROL_NAME" 2>/dev/null) || continue
	___ARISE_CONTROL_FLAGS=${___ARISE_CONTROL_DECLARATION#declare -}
	___ARISE_CONTROL_FLAGS=${___ARISE_CONTROL_FLAGS%% *}
	if [[ $___ARISE_CONTROL_FLAGS == *r* ]]; then
		if [[ ! ${___ARISE_SAVED_SANDBOX_VALUES[$___ARISE_CONTROL_NAME]+present} || ${!___ARISE_CONTROL_NAME} != "${___ARISE_SAVED_SANDBOX_VALUES[$___ARISE_CONTROL_NAME]}" ]]; then
			printf 'ebuild changed readonly isolation control: %s\n' "$___ARISE_CONTROL_NAME"
			return 125
		fi
		continue
	fi
    unset "$___ARISE_CONTROL_NAME" 2>/dev/null || :
  done < <(compgen -v)
  for ___ARISE_CONTROL_NAME in "${!___ARISE_SAVED_SANDBOX_VALUES[@]}"; do
	___ARISE_CONTROL_DECLARATION=$(declare -p "$___ARISE_CONTROL_NAME" 2>/dev/null) || ___ARISE_CONTROL_DECLARATION=
	___ARISE_CONTROL_FLAGS=${___ARISE_CONTROL_DECLARATION#declare -}
	___ARISE_CONTROL_FLAGS=${___ARISE_CONTROL_FLAGS%% *}
	[[ $___ARISE_CONTROL_FLAGS != *r* ]] || continue
    printf -v "$___ARISE_CONTROL_NAME" '%s' "${___ARISE_SAVED_SANDBOX_VALUES[$___ARISE_CONTROL_NAME]}"
    [[ ${___ARISE_SAVED_SANDBOX_EXPORTED[$___ARISE_CONTROL_NAME]-} ]] && export "$___ARISE_CONTROL_NAME"
  done
	return 0
}
___arise_restore_managed_environment() {
	local ___ARISE_RUNTIME_NAME
	[[ -z $___ARISE_SAVED_CATEGORY ]] || CATEGORY=$___ARISE_SAVED_CATEGORY
	[[ -z $___ARISE_SAVED_P ]] || P=$___ARISE_SAVED_P
	[[ -z $___ARISE_SAVED_PF ]] || PF=$___ARISE_SAVED_PF
	[[ -z $___ARISE_SAVED_PN ]] || PN=$___ARISE_SAVED_PN
	[[ -z $___ARISE_SAVED_PV ]] || PV=$___ARISE_SAVED_PV
	[[ -z $___ARISE_SAVED_PR ]] || PR=$___ARISE_SAVED_PR
	[[ -z $___ARISE_SAVED_PVR ]] || PVR=$___ARISE_SAVED_PVR
	[[ $___ARISE_COMMAND == evaluate_metadata || -z $___ARISE_SAVED_SLOT ]] || SLOT=$___ARISE_SAVED_SLOT
  ROOT=$___ARISE_SAVED_ROOT EROOT=$___ARISE_SAVED_EROOT SYSROOT=$___ARISE_SAVED_SYSROOT ESYSROOT=$___ARISE_SAVED_ESYSROOT BROOT=$___ARISE_SAVED_BROOT
  WORKDIR=$___ARISE_SAVED_WORKDIR S=$___ARISE_SAVED_S D=$___ARISE_SAVED_D ED=$___ARISE_SAVED_ED T=$___ARISE_SAVED_T
  FILESDIR=$___ARISE_SAVED_FILESDIR DISTDIR=$___ARISE_SAVED_DISTDIR USE=$___ARISE_SAVED_USE
  HOME=$___ARISE_SAVED_HOME TMPDIR=$___ARISE_SAVED_TMPDIR TMP=$___ARISE_SAVED_TMP TEMP=$___ARISE_SAVED_TEMP
  PORTAGE_BUILDDIR=$___ARISE_SAVED_BUILDDIR PORTAGE_CONFIGROOT=$___ARISE_SAVED_CONFIGROOT PORTAGE_LOG_FILE=$___ARISE_SAVED_LOG_FILE
  export HOME TMPDIR TMP TEMP PORTAGE_BUILDDIR PORTAGE_CONFIGROOT PORTAGE_LOG_FILE
	if [[ ${___ARISE_ENVIRONMENT-} && ! ${___ARISE_ENVIRONMENT_OVERLAY-} ]]; then
		PATH=$___ARISE_SAVED_PATH
		export PATH
		for ___ARISE_RUNTIME_NAME in "${___ARISE_RUNTIME_VARIABLES[@]}"; do
			if [[ ${___ARISE_SAVED_RUNTIME_SETTINGS[$___ARISE_RUNTIME_NAME]+present} ]]; then
				printf -v "$___ARISE_RUNTIME_NAME" '%s' "${___ARISE_SAVED_RUNTIME_SETTINGS[$___ARISE_RUNTIME_NAME]}" || return 125
				export "$___ARISE_RUNTIME_NAME"
			else
				unset -v "$___ARISE_RUNTIME_NAME" || return 125
			fi
		done
	fi
  if [[ $___ARISE_EAPI == 9 ]]; then
    export -n CATEGORY P PF PN PV PR PVR SLOT ROOT EROOT WORKDIR S D ED T FILESDIR DISTDIR USE 2>/dev/null || :
  else
    export CATEGORY P PF PN PV PR PVR SLOT ROOT EROOT WORKDIR S D ED T FILESDIR DISTDIR USE
  fi
  export SYSROOT ESYSROOT BROOT
}
if [[ $___ARISE_EAPI == 9 ]]; then
  (( BASH_VERSINFO[0] > 5 || (BASH_VERSINFO[0] == 5 && BASH_VERSINFO[1] >= 3) )) || {
    printf 'EAPI 9 requires Bash 5.3 or newer\n' >"$___ARISE_LOG_FILE"
    ___ARISE_STATUS=126
  }
  export -n CATEGORY P PF PN PV PR PVR A FILESDIR DISTDIR WORKDIR S ROOT EROOT T EPREFIX D ED USE EBUILD_PHASE EBUILD_PHASE_FUNC MERGE_TYPE REPLACING_VERSIONS REPLACED_BY_VERSION ECLASS INHERITED DEFINED_PHASES ARCH CONFIG_PROTECT CONFIG_PROTECT_MASK USE_EXPAND USE_EXPAND_UNPREFIXED USE_EXPAND_HIDDEN USE_EXPAND_IMPLICIT IUSE_IMPLICIT 2>/dev/null || :
fi
if [[ $___ARISE_COMMAND == evaluate_metadata ]]; then
	unset SLOT
	EBUILD_PHASE=depend
	EBUILD_PHASE_FUNC=
	[[ $___ARISE_EAPI == 9 ]] || export EBUILD_PHASE EBUILD_PHASE_FUNC
elif [[ $___ARISE_COMMAND == run_phase || $___ARISE_COMMAND == run_phases ]]; then
	EBUILD_PHASE_FUNC=${___ARISE_PHASE:-${___ARISE_PHASES%%$'\n'*}}
	EBUILD_PHASE=${EBUILD_PHASE_FUNC#src_}
	EBUILD_PHASE=${EBUILD_PHASE#pkg_}
	if [[ $___ARISE_EAPI == 9 ]]; then
		export -n EBUILD_PHASE EBUILD_PHASE_FUNC
	else
		export EBUILD_PHASE EBUILD_PHASE_FUNC
	fi
fi
if (( ___ARISE_STATUS == 0 )); then
  # A saved environment is the phase's complete package state. Re-sourcing the
  # ebuild would resurrect unset variables/functions and overwrite phase changes.
	# declare -f can contain extended case patterns even when the original
	# function restored extglob after its definition. Enable it only for parsing
	# saved state, then restore the worker's option before executing any hook.
	[[ ! $___ARISE_SNAPSHOT_INPUT ]] || builtin shopt -s extglob
	___ARISE_NORMALIZED_SNAPSHOT=
	if ___arise_source_package_state >>"$___ARISE_LOG_FILE" 2>&1; then
		source "${___ARISE_NORMALIZED_SNAPSHOT:-${___ARISE_ENVIRONMENT_OVERLAY:-${___ARISE_ENVIRONMENT:-$___ARISE_EBUILD}}}" >>"$___ARISE_LOG_FILE" 2>&1
		___ARISE_STATUS=$?
	else
		___ARISE_STATUS=$?
	fi
	[[ ! $___ARISE_NORMALIZED_SNAPSHOT ]] || rm -f -- "$___ARISE_NORMALIZED_SNAPSHOT"
	[[ ! $___ARISE_SNAPSHOT_INPUT ]] || builtin eval "$___ARISE_SNAPSHOT_EXTGLOB"
	if (( ___ARISE_STATUS == 0 )); then
		___arise_load_legacy_helpers >>"$___ARISE_LOG_FILE" 2>&1 || ___ARISE_STATUS=$?
	fi
  for ___ARISE_METADATA_NAME in "${!___ARISE_ECLASS_METADATA[@]}"; do
    printf -v "$___ARISE_METADATA_NAME" '%s%s' "${!___ARISE_METADATA_NAME-}" "${___ARISE_ECLASS_METADATA[$___ARISE_METADATA_NAME]}"
  done
  ___arise_restore_sandbox_environment >>"$___ARISE_LOG_FILE" 2>&1 || ___ARISE_STATUS=$?
  if [[ ${___ARISE_ENVIRONMENT-} && ! ${___ARISE_ENVIRONMENT_OVERLAY-} ]]; then ___ARISE_DECLARED_S=$___ARISE_SAVED_S; else ___ARISE_DECLARED_S=${S-}; fi
fi
if (( ___ARISE_STATUS == 0 )); then
  ___arise_restore_managed_environment >>"$___ARISE_LOG_FILE" 2>&1 || ___ARISE_STATUS=$?
	# Eclasses may add RESTRICT while the ebuild is sourced, after the Go-side
	# static metadata pass. Honor the effective sourced restriction before any
	# image finalization so source packages such as kernel-2 are never stripped.
	if [[ " ${RESTRICT-} " == *" strip "* ]]; then
		___ARISE_STRIP=0
	fi
	S=$___ARISE_DECLARED_S
	[[ $___ARISE_EAPI == 9 ]] || export S
	if [[ $___ARISE_COMMAND != evaluate_metadata ]]; then
		___arise_validate_source_directory >>"$___ARISE_LOG_FILE" 2>&1 || ___ARISE_STATUS=$?
	fi
fi
if (( ___ARISE_STATUS == 0 )) && [[ ${EAPI-} != "$___ARISE_EAPI" ]]; then
  printf 'ebuild EAPI %s does not match preflight EAPI %s\n' "${EAPI-<unset>}" "$___ARISE_EAPI" >>"$___ARISE_LOG_FILE"
  ___ARISE_STATUS=126
fi
___arise_emit_metadata() {
  # Portage persists these two values as derived metadata.  Ebuilds normally do
  # not assign either variable themselves, so derive them after the complete
  # eclass/ebuild source pass rather than overwriting useful cache metadata with
  # an empty string.
  INHERITED=
  if (( ${#___ARISE_INHERITED[@]} )); then
    INHERITED=$(printf '%s\n' "${!___ARISE_INHERITED[@]}" | LC_ALL=C sort | tr '\n' ' ')
    INHERITED=${INHERITED% }
  fi
  ___arise_refresh_defined_phases
  ___ARISE_METADATA_KEYS=( DEPEND RDEPEND BDEPEND IDEPEND PDEPEND IUSE REQUIRED_USE LICENSE PROPERTIES RESTRICT DEFINED_PHASES INHERITED )
  if [[ $___ARISE_COMMAND == evaluate_metadata ]]; then
    ___ARISE_METADATA_KEYS+=( EAPI SLOT KEYWORDS SRC_URI DESCRIPTION HOMEPAGE )
  fi
  for ___ARISE_METADATA_NAME in "${___ARISE_METADATA_KEYS[@]}"; do
    ___ARISE_METADATA_VALUE=${!___ARISE_METADATA_NAME-}
    ___ARISE_METADATA_ESCAPED=$(___arise_escape_json "$___ARISE_METADATA_VALUE")
    ___arise_emit '"kind":"metadata","class":"'"$___ARISE_METADATA_NAME"'","message":"'"$___ARISE_METADATA_ESCAPED"'"'
  done
}
if (( ___ARISE_STATUS == 0 )) && [[ ${___ARISE_EMIT_METADATA-} == 1 || $___ARISE_COMMAND == evaluate_metadata ]]; then
  ___arise_emit_metadata
fi
___arise_run_one_phase() {
  local ___ARISE_RUNNING_PHASE=$1 ___ARISE_PHASE_DIRECTORY ___ARISE_OLD_DIRECTORY=$PWD ___ARISE_PHASE_STATUS=0 ___ARISE_EMPTY_DIRECTORY=
  ___arise_emit '"kind":"phase","message":"'"$___ARISE_RUNNING_PHASE"'"'
  local -r EBUILD_PHASE_FUNC=$___ARISE_RUNNING_PHASE
  local EBUILD_PHASE=${___ARISE_RUNNING_PHASE#src_}
  EBUILD_PHASE=${EBUILD_PHASE#pkg_}
  if [[ $___ARISE_EAPI == 9 ]]; then
	export -n EBUILD_PHASE EBUILD_PHASE_FUNC
  else
	export EBUILD_PHASE EBUILD_PHASE_FUNC
  fi
  ___arise_validate_source_directory || return
  ___ARISE_PHASE_DIRECTORY=${S:-${WORKDIR:-.}}
  case $___ARISE_RUNNING_PHASE in
    src_unpack) ___ARISE_PHASE_DIRECTORY=${WORKDIR:-.} ;;
    pkg_*)
		if [[ $___ARISE_EAPI == 8 || $___ARISE_EAPI == 9 ]]; then
			___ARISE_EMPTY_DIRECTORY=$(mktemp -d "${T:-${WORKDIR:-/tmp}}/.arise-empty.XXXXXXXX") || return
			___ARISE_PHASE_DIRECTORY=$___ARISE_EMPTY_DIRECTORY
		else
			___ARISE_PHASE_DIRECTORY=${WORKDIR:-.}
		fi
		;;
  esac
  if [[ $___ARISE_RUNNING_PHASE == src_* && $___ARISE_RUNNING_PHASE != src_unpack && ! -d $___ARISE_PHASE_DIRECTORY ]]; then
		local ___ARISE_PRIOR_PHASE ___ARISE_HAS_SOURCE_PHASE=0
		for ___ARISE_PRIOR_PHASE in src_unpack src_prepare src_configure src_compile src_test src_install; do
			declare -F "$___ARISE_PRIOR_PHASE" >/dev/null && ___ARISE_HAS_SOURCE_PHASE=1
			[[ $___ARISE_PRIOR_PHASE == "$___ARISE_RUNNING_PHASE" ]] && break
		done
		if [[ -z ${A-} && $___ARISE_HAS_SOURCE_PHASE == 0 ]]; then
			___ARISE_PHASE_DIRECTORY=${WORKDIR:-.}
		else
			printf 'source directory does not exist before %s: %s\n' "$___ARISE_RUNNING_PHASE" "$___ARISE_PHASE_DIRECTORY"
			return 1
		fi
  fi
  cd "$___ARISE_PHASE_DIRECTORY" || return
  # Package descendants must not hold the manager's event pipe open after
  # the worker exits. Bash restores this descriptor after the function call.
  if declare -F "$___ARISE_RUNNING_PHASE" >/dev/null; then
		"$___ARISE_RUNNING_PHASE" 3>&- || ___ARISE_PHASE_STATUS=$?
		# Portage's __ebuild_phase intentionally discards a phase function's
		# ordinary return status. `die` exits the worker shell and remains fatal.
		# Status 125 is reserved for Arise protocol/sandbox safety violations.
		(( ___ARISE_PHASE_STATUS == 125 )) || ___ARISE_PHASE_STATUS=0
  else
    case $___ARISE_RUNNING_PHASE in
      src_unpack|src_prepare|src_configure|src_compile|src_test|src_install|pkg_pretend|pkg_setup|pkg_preinst|pkg_postinst|pkg_prerm|pkg_postrm|pkg_nofetch)
        "default_$___ARISE_RUNNING_PHASE" 3>&- || ___ARISE_PHASE_STATUS=$? ;;
      *) printf 'phase function %s is not defined\n' "$___ARISE_RUNNING_PHASE"; ___ARISE_PHASE_STATUS=127 ;;
    esac
  fi
	if (( ___ARISE_PHASE_STATUS == 0 )); then
		___arise_validate_source_directory || ___ARISE_PHASE_STATUS=$?
	fi
	if (( ___ARISE_PHASE_STATUS == 0 )) && [[ $___ARISE_RUNNING_PHASE == src_install ]]; then
		___arise_run_install_qa_checks || ___ARISE_PHASE_STATUS=$?
	fi
	if (( ___ARISE_PHASE_STATUS == 0 )) && [[ $___ARISE_RUNNING_PHASE == src_install ]]; then
		___arise_finalize_install_image || ___ARISE_PHASE_STATUS=$?
	fi
  cd "$___ARISE_OLD_DIRECTORY" || return
  [[ ! $___ARISE_EMPTY_DIRECTORY ]] || rm -rf -- "$___ARISE_EMPTY_DIRECTORY"
  ___ARISE_LAST_PHASE=$___ARISE_RUNNING_PHASE
  return "$___ARISE_PHASE_STATUS"
}

___arise_save_phase_environment() {
	local ___ARISE_SAVE_OUTPUT=${___ARISE_SAVE_ENVIRONMENT-} ___ARISE_SAVE_TEMPORARY
	[[ $___ARISE_SAVE_OUTPUT ]] || return 0
	[[ ${___ARISE_LAST_PHASE-} != pkg_pretend ]] || return 0
	___ARISE_SAVE_TEMPORARY=$(mktemp "${___ARISE_SAVE_OUTPUT}.tmp.XXXXXXXX") || return
	if ___arise_write_phase_environment >"$___ARISE_SAVE_TEMPORARY" && chmod 0644 "$___ARISE_SAVE_TEMPORARY" && mv -f -- "$___ARISE_SAVE_TEMPORARY" "$___ARISE_SAVE_OUTPUT"; then
		return 0
	fi
	rm -f -- "$___ARISE_SAVE_TEMPORARY"
	return 1
}

if (( ___ARISE_STATUS == 0 )) && [[ $___ARISE_COMMAND == discover_phases ]]; then
  for phase in pkg_pretend pkg_setup src_unpack src_prepare src_configure src_compile src_test src_install pkg_preinst pkg_postinst pkg_prerm pkg_postrm pkg_config pkg_info pkg_nofetch; do
    declare -F "$phase" >/dev/null && ___arise_emit '"kind":"phase","message":"'"$phase"'"'
  done
elif (( ___ARISE_STATUS == 0 )) && [[ $___ARISE_COMMAND == run_phase ]]; then
	  (
		___arise_run_one_phase "$___ARISE_PHASE" || exit $?
		[[ ${___ARISE_EMIT_METADATA-} != 1 ]] || ___arise_emit_metadata
		___arise_save_phase_environment
	  ) >>"$___ARISE_LOG_FILE" 2>&1 || ___ARISE_STATUS=$?
elif (( ___ARISE_STATUS == 0 )) && [[ $___ARISE_COMMAND == run_phases ]]; then
	  (
	    while IFS= read -r ___ARISE_BATCH_PHASE; do
	      [[ $___ARISE_BATCH_PHASE ]] || continue
	      ___arise_run_one_phase "$___ARISE_BATCH_PHASE" || exit $?
	    done <<< "${___ARISE_PHASES-}"
		[[ ${___ARISE_EMIT_METADATA-} != 1 ]] || ___arise_emit_metadata
		___arise_save_phase_environment
	  ) >>"$___ARISE_LOG_FILE" 2>&1 || ___ARISE_STATUS=$?
fi
while IFS= read -r line; do
  if [[ $line == $'\036ARISE_ELOG|'* ]]; then
    payload=${line#$'\036ARISE_ELOG|'}
    class=${payload%%|*}
    message=${payload#*|}
    escaped=$(___arise_escape_json "$message")
    ___arise_emit '"kind":"elog","class":"'"$class"'","message":"'"$escaped"'"'
    continue
  fi
  escaped=$(___arise_escape_json "$line")
  ___arise_emit '"kind":"log","stream":"stdout","message":"'"$escaped"'"'
done <"$___ARISE_LOG_FILE"
rm -f -- "$___ARISE_LOG_FILE"
___ARISE_LOG_FILE=
___ARISE_RESULT_EMITTED=1
___arise_emit '"kind":"result","exit_code":'"$___ARISE_STATUS"
rm -f -- "$___ARISE_SEQUENCE_FILE"
___ARISE_SEQUENCE_FILE=
exit "$___ARISE_STATUS"
