#!/usr/bin/env bash

exec 3>&1
sequence_file=
log_file=
cleanup_worker_temporaries() {
  [[ -z $sequence_file ]] || rm -f -- "$sequence_file"
  [[ -z $log_file ]] || rm -f -- "$log_file"
}
trap cleanup_worker_temporaries EXIT
sequence_file=$(mktemp) || {
  printf 'phase worker: cannot create sequence file\n' >&2
  exit 1
}
printf '0\n' > "$sequence_file"
declare -A ARISE_INHERITING=()
declare -A ARISE_INHERITED=()
escape_json() {
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
emit() {
  local sequence
  sequence=$(<"$sequence_file")
  printf '{"protocol":1,"id":"%s","sequence":%d,%s}\n' "$ARISE_ID" "$sequence" "$1" >&3
  printf '%d\n' "$((sequence+1))" > "$sequence_file"
}
die() { printf '%s\n' "${*:-die called}"; exit 1; }
arise_elog() { local class=$1; shift; printf '\036ARISE_ELOG|%s|%s\n' "$class" "$*"; }
einfo() { arise_elog INFO "$@"; }
elog() { arise_elog LOG "$@"; }
ewarn() { arise_elog WARN "$@"; }
eerror() { arise_elog ERROR "$@"; }
eqawarn() { arise_elog QA "$@"; }
eqatag() {
	[[ ${1-} == -v ]] && shift
	local tag=${1-}; shift || :
	arise_elog QA "${tag}${*:+: $*}"
}
contains_word() { has "$@"; }
find0() { find -files0-from - "$@"; }
arise_add_sandbox_path() {
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
addread() { arise_add_sandbox_path SANDBOX_READ "$@"; }
addwrite() { arise_add_sandbox_path SANDBOX_WRITE "$@"; }
adddeny() { arise_add_sandbox_path SANDBOX_DENY "$@"; }
addpredict() { arise_add_sandbox_path SANDBOX_PREDICT "$@"; }
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
ebegin() { arise_elog INFO "$* ..."; }
eend() {
  local status=${1-} message=${2-}
  [[ $status =~ ^[0-9]+$ ]] || return 2
  if (( status == 0 )); then
    arise_elog INFO "${message:-ok}"
  else
    arise_elog ERROR "${message:-failed}"
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
  done <<< "${ARISE_HAS_VERSION-}"
  # Legacy caller-provided snapshots used an unqualified query key.
  while IFS= read -r line; do
    [[ ${line%%$'\t'*} == "$query" ]] || continue
    [[ ${line##*$'\t'} == 1 ]]
    return
  done <<< "${ARISE_HAS_VERSION-}"
  if [[ ${ARISE_QUERY_HELPER-} ]]; then
    answer=$("$ARISE_QUERY_HELPER" __phase-query has-version "$domain" "$query" "${USE-}") || return 2
    case $answer in
      1) ARISE_HAS_VERSION+="${key}"$'\t1\n'; return 0 ;;
      0) ARISE_HAS_VERSION+="${key}"$'\t0\n'; return 1 ;;
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
  done <<< "${ARISE_BEST_VERSION-}"
  if [[ ${ARISE_QUERY_HELPER-} ]]; then
    answer=$("$ARISE_QUERY_HELPER" __phase-query best-version "$domain" "$query" "${USE-}") || return 2
    ARISE_BEST_VERSION+="${key}"$'\t'"${answer}"$'\n'
    printf '%s\n' "$answer"
    return 0
  fi
  return 0
}
__arise_ver_split() {
  local v=${1-} s c
  ARISE_VER_COMPONENTS=()
  while [[ $v ]]; do
    s=${v%%[a-zA-Z0-9]*}
    v=${v:${#s}}
    [[ $v == [0-9]* ]] && c=${v%%[^0-9]*} || c=${v%%[^a-zA-Z]*}
    v=${v:${#c}}
    ARISE_VER_COMPONENTS+=( "$s" "$c" )
  done
}
__arise_ver_range() {
  local range=${1-} max=${2-}
  [[ $range == [0-9]* ]] || return 2
  ARISE_VER_START=${range%-*}
  [[ $range == *-* ]] && ARISE_VER_END=${range#*-} || ARISE_VER_END=$ARISE_VER_START
  [[ $ARISE_VER_END ]] || ARISE_VER_END=$max
  [[ $ARISE_VER_START -le $ARISE_VER_END ]] || return 2
  (( ARISE_VER_END > max )) && ARISE_VER_END=$max
  return 0
}
ver_cut() {
  local range=${1-} v=${2-${PV-}} max start end IFS=
  [[ $range && $v ]] || return 2
  __arise_ver_split "$v"
  max=$((${#ARISE_VER_COMPONENTS[@]}/2))
  __arise_ver_range "$range" "$max" || return
  start=$ARISE_VER_START end=$ARISE_VER_END
  (( start > 0 )) && start=$((start*2-1))
  printf '%s\n' "${ARISE_VER_COMPONENTS[*]:start:end*2-start}"
}
ver_rs() {
  local v start end i max IFS=
  (( $# & 1 )) && v=${@: -1} || v=${PV-}
  [[ $v ]] || return 2
  __arise_ver_split "$v"
  max=$((${#ARISE_VER_COMPONENTS[@]}/2-1))
  while (( $# >= 2 )); do
    __arise_ver_range "$1" "$max" || return
    start=$ARISE_VER_START end=$ARISE_VER_END
    for ((i=start*2; i<=end*2; i+=2)); do
      [[ $i -eq 0 && -z ${ARISE_VER_COMPONENTS[i]} ]] && continue
      ARISE_VER_COMPONENTS[i]=$2
    done
    shift 2
  done
  printf '%s\n' "${ARISE_VER_COMPONENTS[*]}"
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
DESTTREE=/usr
INSDESTTREE=/
EXEDESTTREE=/usr/bin
DOCDESTTREE=
declare -a INSOPTIONS=(-m0644)
declare -a EXEOPTIONS=(-m0755)
declare -a DIROPTIONS=(-m0755)
declare -a ARISE_DOCOMPRESS=(/usr/share/doc /usr/share/info /usr/share/man)
declare -a ARISE_DOCOMPRESS_SKIP=()
declare -a ARISE_DOSTRIP=(/)
declare -a ARISE_DOSTRIP_SKIP=()
arise_image_path() {
  local path=${1-} component
  local -a components=()
  [[ ${ED-} && $path == /* ]] || { printf 'image helper requires ED and an absolute destination\n'; return 125; }
	IFS=/ read -r -a components <<< "$path"
	for component in "${components[@]}"; do
		[[ $component != .. ]] || { printf 'image helper destination escapes image: %s\n' "$path"; return 125; }
	done
  printf '%s/%s' "${ED%/}" "${path#/}"
}
arise_safe_name() { [[ ${1-} && $1 != */* && $1 != . && $1 != .. ]]; }
arise_install_dir() { install -d "${DIROPTIONS[@]}" -- "$@"; }
into() { [[ ${1-} == /* ]] || { printf 'into requires an absolute prefix\n'; return 1; }; DESTTREE=${1%/}; [[ $DESTTREE ]] || DESTTREE=/; }
insinto() { [[ ${1-} == /* ]] || { printf 'insinto requires absolute image path\n'; return 1; }; INSDESTTREE=$1; }
exeinto() { [[ ${1-} == /* ]] || { printf 'exeinto requires absolute image path\n'; return 1; }; EXEDESTTREE=$1; }
insopts() { (( $# > 0 )) || { printf 'insopts requires options\n'; return 1; }; INSOPTIONS=("$@"); }
exeopts() { (( $# > 0 )) || { printf 'exeopts requires options\n'; return 1; }; EXEOPTIONS=("$@"); }
diropts() { (( $# > 0 )) || { printf 'diropts requires options\n'; return 1; }; DIROPTIONS=("$@"); }
libopts() { printf 'libopts is banned for EAPI %s\n' "${EAPI-unknown}"; return 1; }
dohtml() { printf 'dohtml is banned for EAPI %s\n' "${EAPI-unknown}"; return 1; }
dosed() { printf 'dosed is banned for EAPI %s\n' "${EAPI-unknown}"; return 1; }
dolib() { printf 'dolib is banned for EAPI %s\n' "${EAPI-unknown}"; return 1; }
arise_queue_paths() {
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
      docompress:include) ARISE_DOCOMPRESS+=("$path") ;;
      docompress:exclude) ARISE_DOCOMPRESS_SKIP+=("$path") ;;
      dostrip:include) ARISE_DOSTRIP+=("$path") ;;
      dostrip:exclude) ARISE_DOSTRIP_SKIP+=("$path") ;;
    esac
  done
}
docompress() { arise_queue_paths docompress "$@"; }
dostrip() { arise_queue_paths dostrip "$@"; }
docinto() {
  local path=${1-} component
  local -a components=()
	[[ $path ]] || { printf 'docinto requires a documentation path\n'; return 1; }
	[[ $path == / ]] && { DOCDESTTREE=; return 0; }
	path=${path#/}
  IFS=/ read -r -a components <<< "$path"
  for component in "${components[@]}"; do [[ $component != .. ]] || { printf 'docinto path escapes documentation directory\n'; return 125; }; done
	DOCDESTTREE=$path
}
dobin() {
	[[ ${ED-} ]] || { printf 'dobin requires ED\n'; return 1; }
	(( $# > 0 )) || { printf 'dobin requires files\n'; return 1; }
	local source destination
	destination=$(arise_image_path "$DESTTREE/bin") || return
	arise_install_dir "$destination" || return
	for source in "$@"; do install "${EXEOPTIONS[@]}" -- "$source" "$destination/${source##*/}" || return; done
}
newbin() {
	[[ $# == 2 ]] || { printf 'newbin requires source and name\n'; return 1; }
	local old=$EXEDESTTREE
	EXEDESTTREE=$DESTTREE/bin
	newexe "$@"
	local status=$?
	EXEDESTTREE=$old
	return "$status"
}
dosbin() { local saved=$DESTTREE; into "$DESTTREE" || return; local old=$EXEDESTTREE; EXEDESTTREE=$DESTTREE/sbin; doexe "$@"; local status=$?; EXEDESTTREE=$old; DESTTREE=$saved; return "$status"; }
newsbin() { [[ $# == 2 ]] || { printf 'newsbin requires source and name\n'; return 1; }; local old=$EXEDESTTREE; EXEDESTTREE=$DESTTREE/sbin; newexe "$@"; local status=$?; EXEDESTTREE=$old; return "$status"; }
doexe() {
  (( $# > 0 )) || { printf 'doexe requires files\n'; return 1; }
  local source destination; destination=$(arise_image_path "$EXEDESTTREE") || return
  arise_install_dir "$destination" || return
  for source in "$@"; do install "${EXEOPTIONS[@]}" -- "$source" "$destination/${source##*/}" || return; done
}
newexe() {
  [[ $# == 2 ]] || { printf 'newexe requires source and name\n'; return 1; }
  arise_safe_name "$2" || { printf 'newexe requires a safe name\n'; return 1; }
  local destination source=$1 temporary=''; destination=$(arise_image_path "$EXEDESTTREE") || return
  arise_install_dir "$destination" || return
  if [[ $source == - ]]; then
    temporary=$(mktemp "${T:-${TMPDIR:-/tmp}}/newexe-stdin.XXXXXXXX") || return
    cat > "$temporary" || { rm -f -- "$temporary"; return 1; }
    source=$temporary
  fi
  install "${EXEOPTIONS[@]}" -- "$source" "$destination/$2"
  local status=$?
  [[ -z $temporary ]] || rm -f -- "$temporary"
  return "$status"
}
inherit() {
  local name directory path old_eclass=${ECLASS-}
  for name in "$@"; do
    [[ $name =~ ^[A-Za-z0-9+_.-]+$ ]] || { printf 'unsafe eclass name %s\n' "$name"; return 1; }
    [[ ${ARISE_INHERITED[$name]-} ]] && continue
    [[ ${ARISE_INHERITING[$name]-} ]] && { printf 'circular eclass inherit: %s\n' "$name"; return 1; }
    path=
    while IFS= read -r directory; do
      [[ -f $directory/$name.eclass ]] && { path=$directory/$name.eclass; break; }
    done <<< "${ARISE_ECLASS_DIRS-}"
    [[ $path ]] || { printf 'inherited eclass %s not found\n' "$name"; return 1; }
    ARISE_INHERITING[$name]=1
    ECLASS=$name
    source "$path" || return
    unset 'ARISE_INHERITING[$name]'
    ARISE_INHERITED[$name]=1
    ECLASS=$old_eclass
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
  done <<< "${ARISE_USER_PATCH_DIRS-}"
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
	local recursive=0 source destination; destination=$(arise_image_path "$INSDESTTREE") || return
	[[ ${1-} == -r ]] && { recursive=1; shift; }
	(( $# > 0 )) || { printf 'doins requires files\n'; return 1; }
	arise_install_dir "$destination" || return
	for source in "$@"; do
		if (( recursive )); then cp -R -- "$source" "$destination/" || return
		else install "${INSOPTIONS[@]}" -- "$source" "$destination/" || return; fi
	done
}
newins() {
  [[ $# == 2 ]] || { printf 'newins requires source and name\n'; return 1; }
  arise_safe_name "$2" || { printf 'newins requires a safe name\n'; return 1; }
  local destination source=$1 temporary=''; destination=$(arise_image_path "$INSDESTTREE") || return
  arise_install_dir "$destination" || return
  if [[ $source == - ]]; then
    temporary=$(mktemp "${T:-${TMPDIR:-/tmp}}/newins-stdin.XXXXXXXX") || return
    cat > "$temporary" || { rm -f -- "$temporary"; return 1; }
    source=$temporary
  fi
  install "${INSOPTIONS[@]}" -- "$source" "$destination/$2"
  local status=$?
  [[ -z $temporary ]] || rm -f -- "$temporary"
  return "$status"
}
arise_special_install() {
  local destination=$1 mode=$2 rename=${3-} source target temporary=''
  shift 3
  (( $# > 0 )) || die "installation helper requires files"
  target=$(arise_image_path "$destination") || return
  install -d -m0755 -- "$target" || die "could not create installation directory $destination"
  if [[ $rename ]]; then
    [[ $# == 1 ]] || die "renaming helper requires one source"
    arise_safe_name "$rename" || die "renaming helper requires a safe name"
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
doinitd() { arise_special_install /etc/init.d 0755 '' "$@"; }
newinitd() { [[ $# == 2 ]] || { printf 'newinitd requires source and name\n'; return 1; }; arise_special_install /etc/init.d 0755 "$2" "$1"; }
doconfd() { if [[ ${EAPI-} == 8 ]]; then arise_special_install /etc/conf.d 0644 '' "$@"; else local old=$INSDESTTREE; INSDESTTREE=/etc/conf.d; doins "$@"; local status=$?; INSDESTTREE=$old; return "$status"; fi; }
newconfd() { [[ $# == 2 ]] || { printf 'newconfd requires source and name\n'; return 1; }; if [[ ${EAPI-} == 8 ]]; then arise_special_install /etc/conf.d 0644 "$2" "$1"; else local old=$INSDESTTREE; INSDESTTREE=/etc/conf.d; newins "$@"; local status=$?; INSDESTTREE=$old; return "$status"; fi; }
doenvd() { if [[ ${EAPI-} == 8 ]]; then arise_special_install /etc/env.d 0644 '' "$@"; else local old=$INSDESTTREE; INSDESTTREE=/etc/env.d; doins "$@"; local status=$?; INSDESTTREE=$old; return "$status"; fi; }
newenvd() { [[ $# == 2 ]] || { printf 'newenvd requires source and name\n'; return 1; }; if [[ ${EAPI-} == 8 ]]; then arise_special_install /etc/env.d 0644 "$2" "$1"; else local old=$INSDESTTREE; INSDESTTREE=/etc/env.d; newins "$@"; local status=$?; INSDESTTREE=$old; return "$status"; fi; }
dodir() { local path destination; (( $# > 0 )) || { printf 'dodir requires paths\n'; return 1; }; for path in "$@"; do destination=$(arise_image_path "$path") || return; install -d "${DIROPTIONS[@]}" -- "$destination" || return; done; }
keepdir() {
  local path destination keep=${CATEGORY:-category}_${PN:-package}_${SLOT:-0}
  keep=${keep//\//_}
  for path in "$@"; do dodir "$path" || return; destination=$(arise_image_path "$path") || return; : > "$destination/.keep_$keep" || return; done
}
doheader() { local old=$INSDESTTREE oldopts=("${INSOPTIONS[@]}") olddiropts=("${DIROPTIONS[@]}"); INSDESTTREE=/usr/include; if [[ ${EAPI-} == 8 ]]; then INSOPTIONS=(-m0644); DIROPTIONS=(-m0755); fi; doins "$@"; local status=$?; INSDESTTREE=$old; INSOPTIONS=("${oldopts[@]}"); DIROPTIONS=("${olddiropts[@]}"); return "$status"; }
newheader() { [[ $# == 2 ]] || { printf 'newheader requires source and name\n'; return 1; }; local old=$INSDESTTREE oldopts=("${INSOPTIONS[@]}") olddiropts=("${DIROPTIONS[@]}"); INSDESTTREE=/usr/include; if [[ ${EAPI-} == 8 ]]; then INSOPTIONS=(-m0644); DIROPTIONS=(-m0755); fi; newins "$@"; local status=$?; INSDESTTREE=$old; INSOPTIONS=("${oldopts[@]}"); DIROPTIONS=("${olddiropts[@]}"); return "$status"; }
dolib.a() { local old=$INSDESTTREE oldopts=("${INSOPTIONS[@]}"); INSDESTTREE=$DESTTREE/$(get_libdir); INSOPTIONS=(-m0644); doins "$@"; local status=$?; INSDESTTREE=$old; INSOPTIONS=("${oldopts[@]}"); return "$status"; }
dolib.so() { local old=$INSDESTTREE oldopts=("${INSOPTIONS[@]}"); INSDESTTREE=$DESTTREE/$(get_libdir); INSOPTIONS=(-m0755); doins "$@"; local status=$?; INSDESTTREE=$old; INSOPTIONS=("${oldopts[@]}"); return "$status"; }
newlib.a() { [[ $# == 2 ]] || { printf 'newlib.a requires source and name\n'; return 1; }; local old=$INSDESTTREE oldopts=("${INSOPTIONS[@]}"); INSDESTTREE=$DESTTREE/$(get_libdir); INSOPTIONS=(-m0644); newins "$@"; local status=$?; INSDESTTREE=$old; INSOPTIONS=("${oldopts[@]}"); return "$status"; }
newlib.so() { [[ $# == 2 ]] || { printf 'newlib.so requires source and name\n'; return 1; }; local old=$INSDESTTREE oldopts=("${INSOPTIONS[@]}"); INSDESTTREE=$DESTTREE/$(get_libdir); INSOPTIONS=(-m0755); newins "$@"; local status=$?; INSDESTTREE=$old; INSOPTIONS=("${oldopts[@]}"); return "$status"; }
fperms() {
	[[ ${ED-} ]] || { printf 'fperms requires ED\n'; return 1; }
	local mode=${1-} path destination
	[[ $mode ]] || return 1
	shift
	for path in "$@"; do destination=$(arise_image_path "$path") || return; chmod "$mode" "$destination" || return; done
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
	image_destination=$(arise_image_path "$destination") || return
	arise_install_dir "${image_destination%/*}" || return
	ln -snf -- "$target" "$image_destination"
}
dohard() { printf 'dohard is banned for EAPI %s\n' "${EAPI-unknown}"; return 125; }
fowners() {
  [[ $# -ge 2 ]] || { printf 'fowners requires owner and paths\n'; return 1; }
  local owner=$1 path destination
  shift
  for path in "$@"; do destination=$(arise_image_path "$path") || return; chown -- "$owner" "$destination" || return; done
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
	local recursive=0 destination=$ED/usr/share/doc/${PF:-package}${DOCDESTTREE:+/$DOCDESTTREE} file
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
  arise_safe_name "$2" || { printf 'newdoc requires a safe name\n'; return 1; }
  local old=$INSDESTTREE oldopts=("${INSOPTIONS[@]}") olddiropts=("${DIROPTIONS[@]}"); INSDESTTREE=/usr/share/doc/${PF:-package}${DOCDESTTREE:+/$DOCDESTTREE}; INSOPTIONS=(-m0644); DIROPTIONS=(-m0755); newins "$@"; local status=$?; INSDESTTREE=$old; INSOPTIONS=("${oldopts[@]}"); DIROPTIONS=("${olddiropts[@]}"); return "$status"
}
doman() {
  local file section destination
  for file in "$@"; do
    section=${file##*.}; [[ $section =~ ^[0-9][A-Za-z]*$ ]] || { printf 'doman cannot determine section for %s\n' "$file"; return 1; }
    destination=$(arise_image_path "/usr/share/man/man${section:0:1}") || return
    mkdir -p "$destination" || return
    install -m0644 -- "$file" "$destination/${file##*/}" || return
  done
}
newman() {
  [[ $# == 2 ]] || { printf 'newman requires source and name\n'; return 1; }
  arise_safe_name "$2" || { printf 'newman requires a safe name\n'; return 1; }
  local section=${2##*.} destination
  [[ $section =~ ^[0-9][A-Za-z]*$ ]] || { printf 'newman cannot determine section for %s\n' "$2"; return 1; }
  destination=$(arise_image_path "/usr/share/man/man${section:0:1}") || return
  mkdir -p "$destination" || return
  install -m0644 -- "$1" "$destination/$2"
}
doinfo() { arise_special_install /usr/share/info 0644 '' "$@"; }
domo() {
	[[ ${EAPI-} != 9 ]] || { printf 'domo is banned in EAPI 9\n'; return 1; }
  (( $# > 0 )) || { printf 'domo requires files\n'; return 1; }
  local source locale destination
  for source in "$@"; do
    [[ -f $source ]] || { printf 'domo source not found: %s\n' "$source"; return 1; }
    locale=${source##*/}; locale=${locale%.*}
    destination=$(arise_image_path "/usr/share/locale/$locale/LC_MESSAGES") || return
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
arise_finalize_install_image() {
  [[ ${ED-} ]] || return 0
  local relative path queued skip excluded compressor=${PORTAGE_COMPRESS:-gzip} suffix
  local -a compress_flags=()
	# Portage regenerates the canonical GNU Info directory index on the live
	# system. It must never escape the build image or become package-owned.
	if [[ -d ${ED%/}/usr/share/info ]]; then
		rm -f "${ED%/}"/usr/share/info/dir{,.info}{,.Z,.gz,.bz2,.lzma,.lz,.xz,.zst} || return
	fi
  [[ ${PORTAGE_COMPRESS_FLAGS-} ]] && read -r -a compress_flags <<< "$PORTAGE_COMPRESS_FLAGS"
	case $compressor in gzip) suffix=.gz ;; bzip2) suffix=.bz2 ;; xz) suffix=.xz ;; zstd) suffix=.zst ;; *) printf 'unsupported PORTAGE_COMPRESS: %s\n' "$compressor"; return 1 ;; esac
  for queued in "${ARISE_DOCOMPRESS[@]}"; do
    path=$(arise_image_path "$queued") || return
    [[ -e $path ]] || continue
    while IFS= read -r -d '' path; do
      relative=/${path#"${ED%/}"/}; excluded=0
      for skip in "${ARISE_DOCOMPRESS_SKIP[@]}" "/usr/share/doc/${PF:-package}/html"; do
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
  if [[ ${ARISE_STRIP-0} == 1 ]]; then
    for queued in "${ARISE_DOSTRIP[@]}"; do
      path=$(arise_image_path "$queued") || return
      [[ -e $path ]] || continue
      while IFS= read -r -d '' path; do
        relative=/${path#"${ED%/}"/}; excluded=0
        for skip in "${ARISE_DOSTRIP_SKIP[@]}"; do [[ $relative == "$skip" || $relative == "$skip"/* ]] && { excluded=1; break; }; done
        (( excluded )) && continue
        readelf -h "$path" >/dev/null 2>&1 || continue
        strip --strip-unneeded -- "$path" || return
      done < <(find "$path" -type f -print0)
    done
  fi
	if [[ ${EAPI-} == 8 ]]; then
		find "$ED" -depth -mindepth 1 -type d -empty -delete || return
	fi
	return 0
}
arise_run_install_qa_checks() {
	local check
	: "${PORTAGE_INST_UID:=0}" "${PORTAGE_INST_GID:=0}"
	: "${PORTAGE_TMPDIR:=${WORKDIR%/*}}" "${PORTAGE_PYTHON:=python3}"
	while IFS= read -r check; do
		[[ $check ]] || continue
		if ! source "$check"; then
			eerror "Install QA check ${check##*/} failed to run"
		fi
	done <<< "${ARISE_INSTALL_QA_CHECKS-}"
	return 0
}
log_file=$(mktemp) || {
  printf 'phase worker: cannot create log file\n' >&2
  exit 1
}
status=0
ARISE_DECLARED_S=
readonly ARISE_SAVED_CATEGORY=${CATEGORY-} ARISE_SAVED_P=${P-} ARISE_SAVED_PF=${PF-} ARISE_SAVED_PN=${PN-}
readonly ARISE_SAVED_PV=${PV-} ARISE_SAVED_PR=${PR-} ARISE_SAVED_PVR=${PVR-} ARISE_SAVED_SLOT=${SLOT-}
readonly ARISE_SAVED_ROOT=${ROOT-} ARISE_SAVED_EROOT=${EROOT-} ARISE_SAVED_SYSROOT=${SYSROOT-} ARISE_SAVED_ESYSROOT=${ESYSROOT-} ARISE_SAVED_BROOT=${BROOT-}
readonly ARISE_SAVED_WORKDIR=${WORKDIR-} ARISE_SAVED_S=${S-} ARISE_SAVED_D=${D-} ARISE_SAVED_ED=${ED-} ARISE_SAVED_T=${T-}
readonly ARISE_SAVED_FILESDIR=${FILESDIR-} ARISE_SAVED_DISTDIR=${DISTDIR-} ARISE_SAVED_USE=${USE-}
declare -A ARISE_SAVED_SANDBOX_VALUES=() ARISE_SAVED_SANDBOX_EXPORTED=()
while IFS= read -r name; do
  [[ $name == SANDBOX_* || $name == LD_PRELOAD ]] || continue
  ARISE_SAVED_SANDBOX_VALUES["$name"]=${!name-}
  [[ $(export -p 2>/dev/null) == *"declare -x $name="* || $(export -p 2>/dev/null) == *"declare -x $name" ]] &&
    ARISE_SAVED_SANDBOX_EXPORTED["$name"]=1
done < <(compgen -v)
arise_restore_sandbox_environment() {
  local name
  while IFS= read -r name; do
    [[ $name == SANDBOX_* || $name == LD_PRELOAD ]] || continue
    unset "$name" 2>/dev/null || :
  done < <(compgen -v)
  for name in "${!ARISE_SAVED_SANDBOX_VALUES[@]}"; do
    printf -v "$name" '%s' "${ARISE_SAVED_SANDBOX_VALUES[$name]}"
    [[ ${ARISE_SAVED_SANDBOX_EXPORTED[$name]-} ]] && export "$name"
  done
}
arise_restore_managed_environment() {
	[[ -z $ARISE_SAVED_CATEGORY ]] || CATEGORY=$ARISE_SAVED_CATEGORY
	[[ -z $ARISE_SAVED_P ]] || P=$ARISE_SAVED_P
	[[ -z $ARISE_SAVED_PF ]] || PF=$ARISE_SAVED_PF
	[[ -z $ARISE_SAVED_PN ]] || PN=$ARISE_SAVED_PN
	[[ -z $ARISE_SAVED_PV ]] || PV=$ARISE_SAVED_PV
	[[ -z $ARISE_SAVED_PR ]] || PR=$ARISE_SAVED_PR
	[[ -z $ARISE_SAVED_PVR ]] || PVR=$ARISE_SAVED_PVR
	[[ -z $ARISE_SAVED_SLOT ]] || SLOT=$ARISE_SAVED_SLOT
  ROOT=$ARISE_SAVED_ROOT EROOT=$ARISE_SAVED_EROOT SYSROOT=$ARISE_SAVED_SYSROOT ESYSROOT=$ARISE_SAVED_ESYSROOT BROOT=$ARISE_SAVED_BROOT
  WORKDIR=$ARISE_SAVED_WORKDIR S=$ARISE_SAVED_S D=$ARISE_SAVED_D ED=$ARISE_SAVED_ED T=$ARISE_SAVED_T
  FILESDIR=$ARISE_SAVED_FILESDIR DISTDIR=$ARISE_SAVED_DISTDIR USE=$ARISE_SAVED_USE
  if [[ $ARISE_EAPI == 9 ]]; then
    export -n CATEGORY P PF PN PV PR PVR SLOT ROOT EROOT WORKDIR S D ED T FILESDIR DISTDIR USE 2>/dev/null || :
  else
    export CATEGORY P PF PN PV PR PVR SLOT ROOT EROOT WORKDIR S D ED T FILESDIR DISTDIR USE
  fi
  export SYSROOT ESYSROOT BROOT
}
if [[ $ARISE_EAPI == 9 ]]; then
  (( BASH_VERSINFO[0] > 5 || (BASH_VERSINFO[0] == 5 && BASH_VERSINFO[1] >= 3) )) || {
    printf 'EAPI 9 requires Bash 5.3 or newer\n' >"$log_file"
    status=126
  }
  export -n CATEGORY P PF PN PV PR PVR A FILESDIR DISTDIR WORKDIR S ROOT EROOT T EPREFIX D ED USE EBUILD_PHASE EBUILD_PHASE_FUNC MERGE_TYPE REPLACING_VERSIONS REPLACED_BY_VERSION ECLASS INHERITED DEFINED_PHASES ARCH CONFIG_PROTECT CONFIG_PROTECT_MASK USE_EXPAND USE_EXPAND_UNPREFIXED USE_EXPAND_HIDDEN USE_EXPAND_IMPLICIT IUSE_IMPLICIT 2>/dev/null || :
fi
if (( status == 0 )); then
  source "${ARISE_ENVIRONMENT:-$ARISE_EBUILD}" >"$log_file" 2>&1
  status=$?
  arise_restore_sandbox_environment
  if [[ ${ARISE_ENVIRONMENT-} ]]; then ARISE_DECLARED_S=$ARISE_SAVED_S; else ARISE_DECLARED_S=${S-}; fi
fi
if (( status == 0 )) && [[ ${ARISE_ENVIRONMENT_OVERLAY-} ]]; then
  source "$ARISE_ENVIRONMENT_OVERLAY" >>"$log_file" 2>&1 || status=$?
fi
if (( status == 0 )); then
  arise_restore_managed_environment
	# Eclasses may add RESTRICT while the ebuild is sourced, after the Go-side
	# static metadata pass. Honor the effective sourced restriction before any
	# image finalization so source packages such as kernel-2 are never stripped.
	if [[ " ${RESTRICT-} " == *" strip "* ]]; then
		ARISE_STRIP=0
	fi
	if [[ $ARISE_DECLARED_S != "$ARISE_SAVED_S" && $ARISE_DECLARED_S != "$WORKDIR" && $ARISE_DECLARED_S != "$WORKDIR"/* ]]; then
		printf 'ebuild S escapes WORKDIR: %s\n' "$ARISE_DECLARED_S" >>"$log_file"
		status=126
	else
		S=$ARISE_DECLARED_S
		[[ $ARISE_EAPI == 9 ]] || export S
	fi
fi
if (( status == 0 )) && [[ ${EAPI-} != "$ARISE_EAPI" ]]; then
  printf 'ebuild EAPI %s does not match preflight EAPI %s\n' "${EAPI-<unset>}" "$ARISE_EAPI" >>"$log_file"
  status=126
fi
if (( status == 0 )) && [[ ${ARISE_EMIT_METADATA-} == 1 ]]; then
  # Portage persists these two values as derived metadata.  Ebuilds normally do
  # not assign either variable themselves, so derive them after the complete
  # eclass/ebuild source pass rather than overwriting useful cache metadata with
  # an empty string.
  INHERITED=
  if (( ${#ARISE_INHERITED[@]} )); then
    INHERITED=$(printf '%s\n' "${!ARISE_INHERITED[@]}" | LC_ALL=C sort | tr '\n' ' ')
    INHERITED=${INHERITED% }
  fi
  DEFINED_PHASES=
  for metadata_phase in pkg_pretend pkg_setup src_unpack src_prepare src_configure src_compile src_test src_install pkg_preinst pkg_postinst pkg_prerm pkg_postrm pkg_config pkg_info pkg_nofetch; do
    declare -F "$metadata_phase" >/dev/null && DEFINED_PHASES+="${DEFINED_PHASES:+ }${metadata_phase}"
  done
  for metadata_name in DEPEND RDEPEND BDEPEND IDEPEND PDEPEND IUSE REQUIRED_USE LICENSE PROPERTIES RESTRICT DEFINED_PHASES INHERITED; do
    metadata_value=${!metadata_name-}
    escaped=$(escape_json "$metadata_value")
    emit '"kind":"metadata","class":"'"$metadata_name"'","message":"'"$escaped"'"'
  done
fi
run_one_phase() {
  local phase_name=$1 phase_directory old_directory=$PWD phase_status=0
  emit '"kind":"phase","message":"'"$phase_name"'"'
  EBUILD_PHASE_FUNC=$phase_name
  EBUILD_PHASE=${phase_name#src_}
  EBUILD_PHASE=${EBUILD_PHASE#pkg_}
  export EBUILD_PHASE EBUILD_PHASE_FUNC
  phase_directory=${S:-${WORKDIR:-.}}
  case $phase_name in
    src_unpack|pkg_*) phase_directory=${WORKDIR:-.} ;;
  esac
  cd "$phase_directory" || return
  if declare -F "$phase_name" >/dev/null; then
		"$phase_name" || phase_status=$?
		# Portage's __ebuild_phase intentionally discards a phase function's
		# ordinary return status. `die` exits the worker shell and remains fatal.
		# Status 125 is reserved for Arise protocol/sandbox safety violations.
		(( phase_status == 125 )) || phase_status=0
  else
    case $phase_name in
      src_unpack|src_prepare|src_configure|src_compile|src_test|src_install|pkg_pretend|pkg_setup|pkg_preinst|pkg_postinst|pkg_prerm|pkg_postrm|pkg_nofetch)
        "default_$phase_name" || phase_status=$? ;;
      *) printf 'phase function %s is not defined\n' "$phase_name"; phase_status=127 ;;
    esac
  fi
	if (( phase_status == 0 )) && [[ $phase_name == src_unpack && ${S-} ]]; then
		# Portage leaves S absent while src_unpack runs so custom unpack phases
		# can rename an extracted tree into place, then guarantees the source
		# directory exists before src_prepare. Source-less packages rely on the
		# latter half of that contract.
		mkdir -p -- "$S" || phase_status=$?
	fi
	if (( phase_status == 0 )) && [[ $phase_name == src_install ]]; then
		arise_run_install_qa_checks || phase_status=$?
	fi
	if (( phase_status == 0 )) && [[ $phase_name == src_install ]]; then
		arise_finalize_install_image || phase_status=$?
	fi
  cd "$old_directory" || return
  return "$phase_status"
}

arise_save_phase_environment() {
	local output=${ARISE_SAVE_ENVIRONMENT-} temporary name declaration phase_name
	[[ $output ]] || return 0
	temporary=${output}.tmp.$$
	: >"$temporary" || return
	while IFS= read -r name; do
		case $name in
			ARISE_*|SANDBOX_*|LD_PRELOAD|BASH*|BASHPID|EUID|UID|PPID|SHELLOPTS|FUNCNAME|GROUPS|PIPESTATUS|DIRSTACK|LINENO|RANDOM|SECONDS|SHLVL|_|status|log_file|sequence_file) continue ;;
			CATEGORY|PN|PV|PR|P|PVR|PF|SLOT|ROOT|EROOT|SYSROOT|ESYSROOT|BROOT|WORKDIR|S|D|ED|T|FILESDIR|DISTDIR|HOME|TMPDIR|TMP|TEMP|EBUILD_PHASE|EBUILD_PHASE_FUNC|PORTAGE_LOG_FILE|PORTAGE_BUILDDIR|PORTAGE_CONFIGROOT) continue ;;
		esac
		[[ $name =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || continue
		declaration=$(declare -p "$name" 2>/dev/null) || continue
		[[ $declaration == declare\ -r* || $declaration == declare\ -ar* || $declaration == declare\ -Ar* ]] && continue
		printf '%s\n' "$declaration" >>"$temporary" || return
	done < <(compgen -v | LC_ALL=C sort -u)
	for phase_name in pkg_pretend pkg_setup pkg_preinst pkg_postinst pkg_prerm pkg_postrm pkg_config pkg_info pkg_nofetch; do
		declare -F "$phase_name" >/dev/null || continue
		declare -f "$phase_name" >>"$temporary" || return
	done
	mv -f -- "$temporary" "$output"
}

if (( status == 0 )) && [[ $ARISE_COMMAND == discover_phases ]]; then
  for phase in pkg_pretend pkg_setup src_unpack src_prepare src_configure src_compile src_test src_install pkg_preinst pkg_postinst pkg_prerm pkg_postrm pkg_config pkg_info pkg_nofetch; do
    declare -F "$phase" >/dev/null && emit '"kind":"phase","message":"'"$phase"'"'
  done
elif (( status == 0 )) && [[ $ARISE_COMMAND == run_phase ]]; then
	  ( run_one_phase "$ARISE_PHASE" && arise_save_phase_environment ) >>"$log_file" 2>&1 || status=$?
elif (( status == 0 )) && [[ $ARISE_COMMAND == run_phases ]]; then
	  (
	    while IFS= read -r phase_name; do
	      [[ $phase_name ]] || continue
	      run_one_phase "$phase_name" || exit $?
	    done <<< "${ARISE_PHASES-}"
	  ) >>"$log_file" 2>&1 || status=$?
fi
while IFS= read -r line; do
  if [[ $line == $'\036ARISE_ELOG|'* ]]; then
    payload=${line#$'\036ARISE_ELOG|'}
    class=${payload%%|*}
    message=${payload#*|}
    escaped=$(escape_json "$message")
    emit '"kind":"elog","class":"'"$class"'","message":"'"$escaped"'"'
    continue
  fi
  escaped=$(escape_json "$line")
  emit '"kind":"log","stream":"stdout","message":"'"$escaped"'"'
done <"$log_file"
rm -f -- "$log_file"
log_file=
emit '"kind":"result","exit_code":'"$status"
rm -f -- "$sequence_file"
sequence_file=
exit "$status"
