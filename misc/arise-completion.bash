# arise(1) bash completion — sources this file to enable tab completion
#
# Source in ~/.bashrc or drop in /usr/share/bash-completion/completions/

_arise() {
    local cur prev words cword
    _init_completion -n = || return

    local commands="sync index install update uninstall select deselect recover query state search installed info audit dispatch-conf quickpkg depclean prune env-update ldconfig config news preserved-rebuild revdep-rebuild equery bench"

    local audit_sub="python perl"
    local news_sub="list read display"
    local equery_sub="belongs files uses size check which list"

    if [[ $cword -eq 1 ]]; then
        if [[ $cur == -* ]]; then
            _arise_flags
        else
            COMPREPLY=($(compgen -W "$commands" -- "$cur"))
        fi
        return
    fi

    local cmd="${words[1]}"

    case "$cmd" in
        install|uninstall|update|query|config|select|deselect|quickpkg)
            if [[ $cur == -* ]]; then
                _arise_flags
            else
                _arise_pkg_atoms "$cur"
            fi
            ;;
        equery)
            if [[ $cword -eq 2 ]]; then
                COMPREPLY=($(compgen -W "$equery_sub" -- "$cur"))
            elif [[ $cword -eq 3 ]]; then
                case "${words[2]}" in
                    belongs|files|uses|size|check|which|list)
                        _arise_pkg_atoms "$cur"
                        ;;
                esac
            fi
            ;;
        audit)
            if [[ $cword -eq 2 ]]; then
                COMPREPLY=($(compgen -W "$audit_sub" -- "$cur"))
            fi
            ;;
        news)
            if [[ $cword -eq 2 ]]; then
                COMPREPLY=($(compgen -W "$news_sub" -- "$cur"))
            fi
            ;;
        search)
            if [[ $cur == -* ]]; then
                _arise_flags
            else
                _arise_pkg_atoms "$cur"
            fi
            ;;
        installed)
            COMPREPLY=($(compgen -W "all repo no-repo buildtime no-buildtime --versions --cpv --null -0 -= -q -a" -- "$cur"))
            ;;
        sync|index|state|info|dispatch-conf|depclean|prune|env-update|ldconfig|preserved-rebuild|revdep-rebuild|bench)
            _arise_flags
            ;;
        recover)
            COMPREPLY=($(compgen -W "status rollback --all-active" -- "$cur"))
            ;;
    esac
}

_arise_flags() {
    local flags=(
        -1 -O -o -e -N -D -p -a -q -v -t -b -B -k -K -f -n -g -G -j -l
        --oneshot --nodeps --onlydeps --emptytree --newuse --deep --pretend
        --ask --quiet --verbose --tree --buildpkg --buildpkgonly --usepkg
        --usepkgonly --fetchonly --noreplace --getbinpkg --getbinpkgonly
        --jobs --load-average
        --ask --autounmask-write --backtrack --binpkg-respect-use --buildpkg
        --buildpkgonly --changed-deps --changed-use --color --compact
        --complete-graph --db --deep --desc --deselect --exact
        --fetchonly --getbinpkg --getbinpkgonly
        --ignore-built-slot-operator-deps --jobs --keep-going --load-average
        --name-only --newuse --nodeps --noreplace --oneshot --onlydeps --pretend
        --quiet --regex --reinstall --repo --repo-url --resolver-timeout --resume
        --experimental-live-mutation --approve-plan --approve-plan-sha256
        --save-plan --preflight-only --plan-dir --journal-dir --resume-file
        --work-dir --jobs-tmpdir-require-free-gb --fetch-jobs --show-estimates
        --search-and --search-brief --search-care --search-category
        --search-count-only --search-depends-on --search-dump --search-duplicates
        --search-format --search-has-use --search-has-version --search-installed
        --search-json --search-keywords --search-license --search-masked
        --search-name --search-not --search-only-names --search-overflow
        --search-print --search-required-by --search-slot --search-sort
        --search-stable --search-system --search-testing --search-use
        --search-versions --search-world --skipfirst --tree
        --unordered-display --usepkg --usepkgonly --verbose --with-bdeps
    )
    COMPREPLY=($(compgen -W "${flags[*]}" -- "$cur"))
}

_arise_pkg_atoms() {
    local cur="$1"
    local db="/var/lib/arise/data"
    local repo="/var/db/repos/gentoo"

    if [[ -x ./arise ]]; then
        local arise_cmd=./arise
    elif command -v arise &>/dev/null; then
        local arise_cmd=arise
    else
        _arise_pkg_from_fs "$cur"
        return
    fi

    if [[ -f $db/MANIFEST ]]; then
        local pkgs
        pkgs=$($arise_cmd search --name-only --search-name "$cur" 2>/dev/null | head -200)
        if [[ -n $pkgs ]]; then
            local IFS=$'\n'
            COMPREPLY=($(compgen -W "$pkgs" -- "$cur"))
        fi
    else
        _arise_pkg_from_fs "$cur"
    fi
}

_arise_pkg_from_fs() {
    local cur="$1"
    local repo="/var/db/repos/gentoo"
    local prefix="${cur%%/*}"
    local rest="${cur#*/}"

    if [[ $cur == */* ]]; then
        local dir="$repo/$prefix"
        if [[ -d $dir ]]; then
            local pkgs
            pkgs=$(ls -1 "$dir" 2>/dev/null)
            if [[ -n $pkgs ]]; then
                local IFS=$'\n'
                COMPREPLY=($(compgen -P "$prefix/" -W "$pkgs" -- "$rest"))
            fi
        fi
    else
        local categories
        if [[ -d $repo ]]; then
            categories=$(ls -1d "$repo"/*/ 2>/dev/null | xargs -n1 basename 2>/dev/null)
        fi
        if [[ -n $categories ]]; then
            local IFS=$'\n'
            COMPREPLY=($(compgen -W "$categories" -- "$cur"))
        fi
    fi
}

complete -F _arise arise
