#!/usr/bin/env bash

exec 3>&1
sequence=0
declare -A ARISE_INHERITING=()
declare -A ARISE_INHERITED=()
escape_json() { local s=$1; s=${s//\\/\\\\}; s=${s//\"/\\\"}; printf '%s' "$s"; }
emit() { printf '{"protocol":1,"id":"%s","sequence":%d,%s}\n' "$ARISE_ID" "$sequence" "$1" >&3; sequence=$((sequence+1)); }
die() { printf '%s\n' "${*:-die called}"; return 1; }
has() {
  local needle=${1-} candidate
  (( $# > 0 )) || return 1
  shift
  for candidate in "$@"; do [[ $candidate == "$needle" ]] && return 0; done
  return 1
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
if (( status == 0 )) && [[ $ARISE_COMMAND == discover_phases ]]; then
  for phase in pkg_setup src_unpack src_prepare src_configure src_compile src_test src_install pkg_preinst pkg_postinst pkg_prerm pkg_postrm pkg_config pkg_info pkg_nofetch; do
    declare -F "$phase" >/dev/null && emit '"kind":"phase","message":"'"$phase"'"'
  done
elif (( status == 0 )); then
  emit '"kind":"phase","message":"'"$ARISE_PHASE"'"'
  EBUILD_PHASE=$ARISE_PHASE
  phase_directory=${S:-${WORKDIR:-.}}
  case $ARISE_PHASE in
    src_unpack|pkg_*) phase_directory=${WORKDIR:-.} ;;
  esac
  if declare -F "$ARISE_PHASE" >/dev/null; then
    ( cd "$phase_directory" && "$ARISE_PHASE" ) >>"$log_file" 2>&1 || status=$?
  else
    case $ARISE_PHASE in
      src_unpack|src_prepare|src_configure|src_compile|src_test|src_install|pkg_setup|pkg_preinst|pkg_postinst|pkg_prerm|pkg_postrm)
        ( cd "$phase_directory" && "default_$ARISE_PHASE" ) >>"$log_file" 2>&1 || status=$? ;;
      *) printf 'phase function %s is not defined\n' "$ARISE_PHASE" >>"$log_file"; status=127 ;;
    esac
  fi
fi
while IFS= read -r line; do
  escaped=$(escape_json "$line")
  emit '"kind":"log","stream":"stdout","message":"'"$escaped"'"'
done <"$log_file"
rm -f "$log_file"
emit '"kind":"result","exit_code":'"$status"
exit "$status"
