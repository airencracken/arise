#!/usr/bin/env bash

exec 3>&1
sequence_file=$(mktemp)
printf '0\n' > "$sequence_file"
declare -A ARISE_INHERITING=()
declare -A ARISE_INHERITED=()
escape_json() { local s=$1; s=${s//\\/\\\\}; s=${s//\"/\\\"}; printf '%s' "$s"; }
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
  local flag=${1-}
  [[ $flag ]] || return 1
  [[ " ${USE-} " == *" $flag "* ]]
}
usex() {
  local flag=${1-} yes=${2-yes} no=${3-no}
  use "$flag" && printf '%s' "$yes" || printf '%s' "$no"
}
has_version() {
  while [[ ${1-} == -* ]]; do shift; done
  local query=${1-} line
  [[ $query ]] || return 2
  while IFS= read -r line; do
    [[ ${line%%$'\t'*} == "$query" ]] || continue
    [[ ${line##*$'\t'} == 1 ]]
    return
  done <<< "${ARISE_HAS_VERSION-}"
  printf 'has_version query was not preflighted: %s\n' "$query"
  return 2
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
get_libdir() {
  case ${ABI-} in
    amd64|x86_64) printf 'lib64' ;;
    x86) printf 'lib32' ;;
    *) [[ ${DEFAULT_ABI-} == amd64 ]] && printf 'lib64' || printf 'lib' ;;
  esac
}
dobin() {
  [[ ${ED-} ]] || { printf 'dobin requires ED\n'; return 1; }
  local source destination=$ED/usr/bin
  mkdir -p "$destination" || return
  for source in "$@"; do install -m 0755 -- "$source" "$destination/${source##*/}" || return; done
}
newbin() {
  [[ $# == 2 ]] || { printf 'newbin requires source and name\n'; return 1; }
  [[ ${ED-} ]] || { printf 'newbin requires ED\n'; return 1; }
  mkdir -p "$ED/usr/bin" || return
  install -m 0755 -- "$1" "$ED/usr/bin/$2"
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
  local patch_file
  for patch_file in "$@"; do
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
      *.tar.gz|*.tgz) tar -xzf "$source" || return ;;
      *.tar.bz2|*.tbz2) tar -xjf "$source" || return ;;
      *.tar.xz|*.txz) tar -xJf "$source" || return ;;
      *.tar.zst|*.tzst) tar --zstd -xf "$source" || return ;;
      *.tar) tar -xf "$source" || return ;;
      *.gz) basename=${archive##*/}; output=${basename%.gz}; gzip -dc "$source" > "$output" || return ;;
      *.bz2) basename=${archive##*/}; output=${basename%.bz2}; bzip2 -dc "$source" > "$output" || return ;;
      *.xz) basename=${archive##*/}; output=${basename%.xz}; xz -dc "$source" > "$output" || return ;;
      *.zip) unzip -qo "$source" || return ;;
      *.deb) ar p "$source" data.tar.xz | tar -xJf - || ar p "$source" data.tar.zst | tar --zstd -xf - || return ;;
      *) printf 'unpack: unsupported archive: %s\n' "$archive"; return 1 ;;
    esac
  done
}
INSDESTTREE=/
insinto() { [[ ${1-} == /* ]] || { printf 'insinto requires absolute image path\n'; return 1; }; INSDESTTREE=$1; }
doins() {
  [[ ${ED-} ]] || { printf 'doins requires ED\n'; return 1; }
  local recursive=0 source destination=$ED/${INSDESTTREE#/}
  [[ ${1-} == -r ]] && { recursive=1; shift; }
  mkdir -p "$destination" || return
  for source in "$@"; do
    if (( recursive )); then cp -R -- "$source" "$destination/" || return
    else install -m 0644 -- "$source" "$destination/" || return; fi
  done
}
fperms() {
  [[ ${ED-} ]] || { printf 'fperms requires ED\n'; return 1; }
  local mode=${1-} path
  [[ $mode ]] || return 1
  shift
  for path in "$@"; do chmod "$mode" "$ED/${path#/}" || return; done
}
dosym() {
  [[ ${ED-} ]] || { printf 'dosym requires ED\n'; return 1; }
  local target=${1-} destination=${2-}
  [[ $target && $destination == /* ]] || { printf 'dosym requires target and absolute destination\n'; return 1; }
  mkdir -p "$(dirname "$ED/${destination#/}")" || return
  ln -snf -- "$target" "$ED/${destination#/}"
}
emake() {
  local -a makeopts=()
  [[ ${MAKEOPTS-} ]] && read -r -a makeopts <<< "$MAKEOPTS"
  "${MAKE:-make}" "${makeopts[@]}" ${EXTRA_EMAKE-} "$@"
}
econf() {
  local source=${ECONF_SOURCE:-.}
  "$source/configure" --prefix=/usr "$@"
}
dodoc() {
  [[ ${ED-} ]] || { printf 'dodoc requires ED\n'; return 1; }
  local destination=$ED/usr/share/doc/${PF:-package} file
  mkdir -p "$destination" || return
  for file in "$@"; do cp -R -- "$file" "$destination/" || return; done
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
default_pkg_preinst() { :; }
default_pkg_postinst() { :; }
default_pkg_prerm() { :; }
default_pkg_postrm() { :; }
default() {
  local implementation=default_${EBUILD_PHASE-}
  declare -F "$implementation" >/dev/null || { printf 'default phase unavailable: %s\n' "${EBUILD_PHASE-}"; return 1; }
  "$implementation" "$@"
}
log_file=$(mktemp)
status=0
source "$ARISE_EBUILD" >"$log_file" 2>&1
status=$?
if (( status == 0 )) && [[ ${EAPI-} != "$ARISE_EAPI" ]]; then
  printf 'ebuild EAPI %s does not match preflight EAPI %s\n' "${EAPI-<unset>}" "$ARISE_EAPI" >>"$log_file"
  status=126
fi
run_one_phase() {
  local phase_name=$1 phase_directory old_directory=$PWD phase_status=0
  emit '"kind":"phase","message":"'"$phase_name"'"'
  EBUILD_PHASE=$phase_name
  phase_directory=${S:-${WORKDIR:-.}}
  case $phase_name in
    src_unpack|pkg_*) phase_directory=${WORKDIR:-.} ;;
  esac
  cd "$phase_directory" || return
  if declare -F "$phase_name" >/dev/null; then
    "$phase_name" || phase_status=$?
  else
    case $phase_name in
      src_unpack|src_prepare|src_configure|src_compile|src_test|src_install|pkg_setup|pkg_preinst|pkg_postinst|pkg_prerm|pkg_postrm)
        "default_$phase_name" || phase_status=$? ;;
      *) printf 'phase function %s is not defined\n' "$phase_name"; phase_status=127 ;;
    esac
  fi
  cd "$old_directory" || return
  return "$phase_status"
}

if (( status == 0 )) && [[ $ARISE_COMMAND == discover_phases ]]; then
  for phase in pkg_setup src_unpack src_prepare src_configure src_compile src_test src_install pkg_preinst pkg_postinst pkg_prerm pkg_postrm pkg_config pkg_info pkg_nofetch; do
    declare -F "$phase" >/dev/null && emit '"kind":"phase","message":"'"$phase"'"'
  done
elif (( status == 0 )) && [[ $ARISE_COMMAND == run_phase ]]; then
	  ( run_one_phase "$ARISE_PHASE" ) >>"$log_file" 2>&1 || status=$?
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
rm -f "$log_file"
emit '"kind":"result","exit_code":'"$status"
rm -f "$sequence_file"
exit "$status"
