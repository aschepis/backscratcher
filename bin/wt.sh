#!/usr/bin/env bash
# wt — a single, first-class git worktree CLI.
#
# This file is meant to be SOURCED, not executed, so that `wt switch`/`wt new`
# can change the *current* shell's directory (no nested-shell `exec $SHELL` hack).
#
#   # ~/.zshrc (or ~/.bashrc)
#   source /path/to/backscratcher/bin/wt.sh
#
# Then:  wt              fuzzy-switch worktrees
#        wt new <branch> create a worktree (sibling dir), optionally cd into it
#        wt ls           list worktrees with safety status
#        wt rm [name]    remove a worktree (safety-gated)
#        wt clean        batch-remove all safe worktrees
#        wt doctor       check your setup
#
# Supersedes the older cdwt/mkwt/lswt/rmwt/cleanwt scripts (which remain in bin/).
#
# Optional config: ~/.config/wt/config  (KEY=value lines). See `wt help`.
# Works in both zsh (primary) and bash. Uses fzf when available, falls back to
# a numbered menu otherwise. Uses gh (if authenticated) to detect squash-merges.

# --------------------------------------------------------------------------
# Config
# --------------------------------------------------------------------------

_wt_load_config() {
    local f="${WT_CONFIG:-$HOME/.config/wt/config}"
    [ -r "$f" ] || return 0
    local line key val
    while IFS= read -r line; do
        case "$line" in ''|'#'*) continue ;; esac
        key="${line%%=*}"
        val="${line#*=}"
        # strip optional surrounding single/double quotes
        case "$val" in
            \"*\") val="${val#\"}"; val="${val%\"}" ;;
            \'*\') val="${val#\'}"; val="${val%\'}" ;;
        esac
        case "$key" in
            WT_*)
                # env wins over config: only set if currently empty/unset
                if eval "[ -z \"\${$key:-}\" ]"; then
                    eval "$key=\$val"
                fi
                ;;
        esac
    done < "$f"
}

# --------------------------------------------------------------------------
# Repo / environment setup (called by every subcommand)
# --------------------------------------------------------------------------

_wt_repo_setup() {
    if ! git rev-parse --git-dir >/dev/null 2>&1; then
        echo "wt: not in a git repository" >&2
        return 1
    fi
    _wt_main_branch="${WT_DEFAULT_BRANCH:-$(git symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's@^refs/remotes/origin/@@')}"
    [ -n "$_wt_main_branch" ] || _wt_main_branch="main"
    if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
        _wt_has_gh=1
    else
        _wt_has_gh=0
    fi
    return 0
}

_wt_main_path() {
    git worktree list --porcelain 2>/dev/null | sed -n 's/^worktree //p' | head -1
}

# --------------------------------------------------------------------------
# Worktree enumeration — parse `git worktree list --porcelain` once.
# Emits one TAB-separated record per worktree:
#   PATH \t BRANCH \t ISMAIN \t DETACHED \t BARE \t LOCKED
# The main worktree is always git's first record.
# --------------------------------------------------------------------------

_wt_list_records() {
    # Note: avoid the local names `path` and `status` — both are special in zsh
    # (`local path` unties $PATH; `status` is read-only).
    local wtp="" branch="" detached=0 bare=0 locked=0 first=1 ismain=0 line
    while IFS= read -r line; do
        case "$line" in
            "worktree "*)           wtp="${line#worktree }" ;;
            "branch refs/heads/"*)  branch="${line#branch refs/heads/}" ;;
            "detached")             detached=1 ;;
            "bare")                 bare=1 ;;
            "locked"*)              locked=1 ;;
            "")
                if [ -n "$wtp" ]; then
                    if [ "$first" = 1 ]; then ismain=1; first=0; else ismain=0; fi
                    printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$wtp" "$branch" "$ismain" "$detached" "$bare" "$locked"
                fi
                wtp=""; branch=""; detached=0; bare=0; locked=0
                ;;
        esac
    done <<EOF
$(git worktree list --porcelain 2>/dev/null)
EOF
    # Flush the final record (command substitution strips the trailing blank line).
    if [ -n "$wtp" ]; then
        if [ "$first" = 1 ]; then ismain=1; else ismain=0; fi
        printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$wtp" "$branch" "$ismain" "$detached" "$bare" "$locked"
    fi
}

# --------------------------------------------------------------------------
# Consolidated safety check (the one code path shared by ls / rm / clean).
# Echoes "STATUS|reasons" where STATUS is one of:
#   main    not removable (main worktree or bare)
#   missing path is gone — removable, no force needed
#   safe    clean & merged — removable
#   unsafe  has issues (reasons listed) — removable only with force/strict confirm
# Requires _wt_main_branch and _wt_has_gh to be set (via _wt_repo_setup).
# --------------------------------------------------------------------------

_wt_safety() {
    local wtp="$1" branch="$2" ismain="$3" detached="$4" bare="$5"
    if [ "$ismain" = 1 ] || [ "$bare" = 1 ]; then
        echo "main|"
        return
    fi
    if [ ! -d "$wtp" ]; then
        echo "missing|path missing"
        return
    fi

    local issues=""

    # Uncommitted changes
    if [ -n "$(git -C "$wtp" status --porcelain 2>/dev/null)" ]; then
        issues="uncommitted changes"
    fi

    if [ -n "$branch" ] && [ "$detached" != 1 ]; then
        # Unpushed commits (only meaningful if the branch has an origin upstream)
        if git -C "$wtp" rev-parse --verify --quiet "origin/$branch" >/dev/null 2>&1; then
            local n
            n=$(git -C "$wtp" rev-list --count "origin/$branch..$branch" 2>/dev/null)
            if [ -n "$n" ] && [ "$n" -gt 0 ]; then
                issues="${issues:+$issues, }$n unpushed commit(s)"
            fi
        else
            issues="${issues:+$issues, }not pushed"
        fi

        # Merged into main? (handles regular merges; falls back to gh for squash-merges)
        if [ "$branch" != "$_wt_main_branch" ]; then
            if git -C "$wtp" merge-base --is-ancestor "$branch" "origin/$_wt_main_branch" 2>/dev/null; then
                :
            elif [ "$_wt_has_gh" = 1 ] && gh pr view "$branch" --json state --jq '.state' 2>/dev/null | grep -q "MERGED"; then
                :
            else
                issues="${issues:+$issues, }not merged to $_wt_main_branch"
            fi
        fi
    fi

    if [ -z "$issues" ]; then
        echo "safe|"
    else
        echo "unsafe|$issues"
    fi
}

# --------------------------------------------------------------------------
# Rendering helpers
# --------------------------------------------------------------------------

_wt_color() {
    # _wt_color <name> <text...>  — colors only when _wt_use_color=1
    local name="$1"; shift
    if [ "${_wt_use_color:-0}" = 1 ]; then
        local code
        case "$name" in
            green)  code='0;32' ;;
            yellow) code='0;33' ;;
            red)    code='0;31' ;;
            dim)    code='2' ;;
            *)      code='0' ;;
        esac
        printf '\033[%sm%s\033[0m' "$code" "$*"
    else
        printf '%s' "$*"
    fi
}

_wt_set_color() {
    if [ -t 1 ] && [ "${WT_NO_COLOR:-${NO_COLOR:-0}}" != 1 ] && [ -z "${NO_COLOR:-}" ]; then
        _wt_use_color=1
    else
        _wt_use_color=0
    fi
}

# Emit "RENDER \t PATH" rows for the fuzzy picker.
#   $1 = exclude_main (1 to drop the main worktree, e.g. for rm)
_wt_render_rows() {
    local exclude_main="$1" wtp branch ismain detached bare locked
    local res safety reasons marker
    while IFS=$'\t' read -r wtp branch ismain detached bare locked; do
        [ "$exclude_main" = 1 ] && [ "$ismain" = 1 ] && continue
        [ "$bare" = 1 ] && continue
        res=$(_wt_safety "$wtp" "$branch" "$ismain" "$detached" "$bare")
        safety="${res%%|*}"; reasons="${res#*|}"
        case "$safety" in
            main)    marker="(main)" ;;
            safe)    marker="✓" ;;
            missing) marker="⚠ path missing" ;;
            unsafe)  marker="⚠ $reasons" ;;
        esac
        printf '%s  [%s]  %s\t%s\n' "$(basename "$wtp")" "${branch:-detached}" "$marker" "$wtp"
    done <<EOF
$(_wt_list_records)
EOF
}

# --------------------------------------------------------------------------
# Picker: fzf when available, numbered `select` fallback otherwise.
# Reads "RENDER \t PATH" rows on stdin, prints the chosen PATH on stdout.
# All UI goes to the tty/stderr so the path can be captured via $(...).
# --------------------------------------------------------------------------

# Case-insensitive substring test. Neither ${var,,} (bash 4+) nor ${var:l}
# (zsh) exists in both shells, so lowercase through tr.
_wt_matches() {
    local haystack needle
    haystack=$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')
    needle=$(printf '%s' "$2" | tr '[:upper:]' '[:lower:]')
    case "$haystack" in *"$needle"*) return 0 ;; esac
    return 1
}

_wt_no_match() {
    if [ -n "$1" ]; then
        echo "wt: no worktree matching '$1'" >&2
    else
        echo "wt: no worktrees found" >&2
    fi
}

_wt_pick() {
    local header="$1" query="$2" out fzf_status
    if [ "${WT_USE_FZF:-1}" != 0 ] && command -v fzf >/dev/null 2>&1; then
        out=$(fzf --ansi --delimiter=$'\t' --with-nth=1 --nth=1 \
                  --query="$query" --select-1 --exit-0 \
                  --height=45% --reverse --header="$header" \
                  --preview='git -C {2} log --oneline -10 2>/dev/null; echo; git -C {2} status -s 2>/dev/null' \
                  --preview-window='right,55%,wrap')
        fzf_status=$?
        # fzf exits 1 when --exit-0 matches nothing, 130 when the user aborts.
        [ "$fzf_status" -eq 1 ] && return 1
        [ -n "$out" ] && printf '%s' "${out#*$'\t'}"
        return 0
    fi

    # Fallback: numbered menu on the tty.
    local -a _renders _paths
    local r p
    while IFS=$'\t' read -r r p; do
        if [ -n "$query" ] && ! _wt_matches "$r" "$query"; then
            continue
        fi
        _renders+=("$r")
        _paths+=("$p")
    done
    [ "${#_paths[@]}" -gt 0 ] || return 1

    if [ "${#_paths[@]}" -eq 1 ]; then
        # Avoid indexing: zsh arrays are 1-based, bash arrays 0-based.
        for p in "${_paths[@]}"; do printf '%s' "$p"; done
        return 0
    fi

    {
        echo "$header" >&2
        local PS3="Selection (number, or 'q' to quit): "
        local choice
        select choice in "${_renders[@]}"; do
            if [ "$REPLY" = q ]; then return 0; fi
            if [ -n "$choice" ]; then
                # Map REPLY (1-based in both shells) to a path by counting.
                local i=1
                for p in "${_paths[@]}"; do
                    if [ "$i" = "$REPLY" ]; then printf '%s' "$p"; return 0; fi
                    i=$((i + 1))
                done
            fi
            echo "Invalid selection." >&2
        done
    } </dev/tty
}

# Resolve an exact name/branch/path argument to a worktree path.
#   $1 = query, $2 = exclude_main (1 to skip the main worktree)
_wt_resolve_name() {
    local q="$1" exclude_main="$2" wtp branch ismain detached bare locked
    while IFS=$'\t' read -r wtp branch ismain detached bare locked; do
        [ "$exclude_main" = 1 ] && [ "$ismain" = 1 ] && continue
        if [ "$wtp" = "$q" ] || [ "$(basename "$wtp")" = "$q" ] || [ "$branch" = "$q" ]; then
            printf '%s' "$wtp"
            return 0
        fi
    done <<EOF
$(_wt_list_records)
EOF
    return 1
}

# Resolve a query to a worktree path: exact match first, otherwise hand the
# query to the picker, which resolves a unique fuzzy match without any UI.
#   $1 = query (may be empty), $2 = exclude_main, $3 = picker header
_wt_find() {
    local q="$1" exclude_main="$2" header="$3" wtp
    if [ -n "$q" ]; then
        wtp=$(_wt_resolve_name "$q" "$exclude_main")
        if [ -n "$wtp" ]; then
            printf '%s' "$wtp"
            return 0
        fi
    fi
    _wt_render_rows "$exclude_main" | _wt_pick "$header" "$q"
}

# --------------------------------------------------------------------------
# wt ls
# --------------------------------------------------------------------------

_wt_cmd_ls() {
    _wt_repo_setup || return 1
    _wt_set_color
    local wtp branch ismain detached bare locked
    local res safety reasons st
    printf '%-50s %-22s %s\n' "WORKTREE" "BRANCH" "STATUS"
    printf '%s\n' "------------------------------------------------------------------------------------------"
    while IFS=$'\t' read -r wtp branch ismain detached bare locked; do
        res=$(_wt_safety "$wtp" "$branch" "$ismain" "$detached" "$bare")
        safety="${res%%|*}"; reasons="${res#*|}"
        case "$safety" in
            main)    st="$(_wt_color dim '(main worktree)')" ;;
            safe)    st="$(_wt_color green '✓ safe to delete')" ;;
            missing) st="$(_wt_color yellow '⚠ path missing')" ;;
            unsafe)  st="$(_wt_color yellow "⚠ $reasons")" ;;
        esac
        printf '%-50s %-22s %s\n' "$wtp" "${branch:-(detached)}" "$st"
    done <<EOF
$(_wt_list_records)
EOF
}

# --------------------------------------------------------------------------
# wt switch / cd
# --------------------------------------------------------------------------

_wt_cmd_switch() {
    _wt_repo_setup || return 1
    local target="$1" wtp
    wtp=$(_wt_find "$target" 0 "Switch to which worktree?") || { _wt_no_match "$target"; return 1; }
    [ -n "$wtp" ] || return 0
    if [ ! -d "$wtp" ]; then
        echo "wt: path no longer exists: $wtp" >&2
        return 1
    fi
    builtin cd "$wtp" || return 1
}

# --------------------------------------------------------------------------
# wt new / add
# --------------------------------------------------------------------------

_wt_post_create() {
    local src="$1" dst="$2"
    [ -n "$ZSH_VERSION" ] && setopt local_options null_glob 2>/dev/null

    if [ -n "${WT_COPY_UNTRACKED:-}" ]; then
        local glob f
        while IFS= read -r glob; do
            [ -n "$glob" ] || continue
            for f in "$src"/$glob; do
                [ -e "$f" ] || continue
                if cp -Rp "$f" "$dst"/ 2>/dev/null; then
                    echo "  copied $(basename "$f")"
                fi
            done
        done <<EOF
$(printf '%s\n' "$WT_COPY_UNTRACKED" | tr ' ' '\n')
EOF
    fi

    if [ -n "${WT_POST_CREATE:-}" ]; then
        echo "  running post-create hook..."
        ( builtin cd "$dst" && eval "$WT_POST_CREATE" )
    fi
}

_wt_maybe_cd() {
    local dst="$1" ans
    if [ "${WT_AUTO_CD:-0}" = 1 ]; then
        builtin cd "$dst"
        return
    fi
    printf 'Switch to it? (y/n): '
    read -r ans
    case "$ans" in y|yes) builtin cd "$dst" ;; esac
}

_wt_cmd_new() {
    _wt_repo_setup || return 1

    local new_branch=0 base_branch="" target="" custom_path=""
    while [ "$#" -gt 0 ]; do
        case "$1" in
            -b)
                new_branch=1
                shift
                ;;
            --from)
                # Guard the missing value: a bare `shift 2` here would loop forever.
                if [ -z "$2" ]; then
                    echo "wt: --from requires a branch name" >&2
                    return 1
                fi
                base_branch="$2"
                shift 2
                ;;
            *)
                if [ -z "$target" ]; then
                    target="$1"
                elif [ -z "$custom_path" ]; then
                    custom_path="$1"
                fi
                shift
                ;;
        esac
    done

    if [ -z "$target" ]; then
        echo "Usage: wt new <branch> [path]                  create worktree for a branch"
        echo "       wt new -b <new-branch> [path]           create worktree with a new branch"
        echo "       wt new -b <new-branch> --from <base>    branch off <base> instead of HEAD"
        return 1
    fi

    if [ -n "$base_branch" ] && [ "$new_branch" != 1 ]; then
        echo "wt: --from only applies to a new branch (add -b)" >&2
        return 1
    fi

    local repo_root repo_name base branch safe_branch wtname wtpath
    repo_root=$(_wt_main_path)
    repo_name=$(basename "$repo_root")
    base="${WT_BASE_DIR:-$(dirname "$repo_root")}"
    branch="$target"
    safe_branch="${branch//\//-}"            # feature/x -> feature-x for the dir name
    wtname="${repo_name}-${safe_branch}"
    wtpath="${custom_path:-$base/$wtname}"

    if [ -d "$wtpath" ]; then
        echo "Worktree already exists at: $wtpath"
        _wt_maybe_cd "$wtpath"
        return 0
    fi

    local start_point=""
    if [ -n "$base_branch" ]; then
        # Fetch first so the new branch starts from an up-to-date base.
        git fetch origin "$base_branch" >/dev/null 2>&1 || true
        if git show-ref --verify --quiet "refs/heads/$base_branch"; then
            start_point="$base_branch"
        elif git show-ref --verify --quiet "refs/remotes/origin/$base_branch"; then
            start_point="origin/$base_branch"
        else
            echo "wt: base branch '$base_branch' not found locally or on origin" >&2
            return 1
        fi
    fi

    echo ""
    if [ "$new_branch" = 1 ]; then
        if [ -n "$start_point" ]; then
            echo "Creating worktree with new branch '$branch' from '$start_point'..."
            git worktree add -b "$branch" "$wtpath" "$start_point" || return 1
        else
            echo "Creating worktree with new branch '$branch'..."
            git worktree add -b "$branch" "$wtpath" || return 1
        fi
    else
        git fetch origin "$branch" 2>/dev/null || true
        if git show-ref --verify --quiet "refs/heads/$branch" \
           || git show-ref --verify --quiet "refs/remotes/origin/$branch"; then
            echo "Creating worktree for branch '$branch'..."
            git worktree add "$wtpath" "$branch" || return 1
        else
            echo "Branch '$branch' not found locally or on origin."
            local ans
            printf 'Create new branch? (y/n): '
            read -r ans
            case "$ans" in
                y|yes) git worktree add -b "$branch" "$wtpath" || return 1 ;;
                *) return 1 ;;
            esac
        fi
    fi

    echo ""
    echo "✓ Worktree created at: $wtpath"
    _wt_post_create "$repo_root" "$wtpath"
    echo ""
    _wt_maybe_cd "$wtpath"
}

# --------------------------------------------------------------------------
# Shared removal helpers (used by rm and clean)
# --------------------------------------------------------------------------

_wt_offer_branch_delete() {
    local branch="$1" ans
    [ -n "$branch" ] || return 0
    printf "Also delete branch '%s'? (y/n): " "$branch"
    read -r ans
    case "$ans" in y|yes) ;; *) return 0 ;; esac
    if git branch -d "$branch" 2>/dev/null; then
        echo "✓ Branch deleted."
    else
        printf 'Branch not fully merged. Force delete? (y/n): '
        read -r ans
        case "$ans" in
            y|yes) git branch -D "$branch" && echo "✓ Branch force deleted." ;;
        esac
    fi
}

_wt_remove_one() {
    local wtp="$1" branch="$2"
    local res safety reasons force=""
    res=$(_wt_safety "$wtp" "$branch" 0 0 0)
    safety="${res%%|*}"; reasons="${res#*|}"

    echo ""
    echo "Worktree: $wtp"
    echo "Branch:   ${branch:-(detached)}"

    local ans
    if [ "$safety" = unsafe ]; then
        echo "Status:   ⚠ $reasons"
        echo ""
        printf 'This worktree has issues. Delete anyway? (yes/no): '
        read -r ans
        [ "$ans" = yes ] || { echo "Cancelled."; return 0; }
        force="--force"
    else
        if [ "$safety" = missing ]; then
            echo "Status:   path missing"
            force="--force"
        else
            echo "Status:   ✓ safe to delete"
        fi
        echo ""
        printf 'Delete this worktree? (y/n): '
        read -r ans
        case "$ans" in y|yes) ;; *) echo "Cancelled."; return 0 ;; esac
    fi

    echo ""
    echo "Removing worktree..."
    if git worktree remove $force "$wtp"; then
        echo "✓ Worktree removed."
        _wt_offer_branch_delete "$branch"
    else
        echo "✗ Failed to remove worktree." >&2
        return 1
    fi
}

# --------------------------------------------------------------------------
# wt rm
# --------------------------------------------------------------------------

_wt_cmd_rm() {
    _wt_repo_setup || return 1
    local target="$1" wtp branch rows
    rows=$(_wt_render_rows 1)
    if [ -z "$rows" ]; then
        echo "No removable worktrees found."
        return 0
    fi
    if [ -n "$target" ]; then
        wtp=$(_wt_resolve_name "$target" 1)
    fi
    if [ -z "$wtp" ]; then
        wtp=$(printf '%s\n' "$rows" | _wt_pick "Select a worktree to delete" "$target") \
            || { _wt_no_match "$target"; return 1; }
    fi
    if [ -z "$wtp" ]; then
        echo "Cancelled."
        return 0
    fi
    branch=$(git -C "$wtp" symbolic-ref --quiet --short HEAD 2>/dev/null)
    _wt_remove_one "$wtp" "$branch"
}

# --------------------------------------------------------------------------
# wt clean
# --------------------------------------------------------------------------

_wt_cmd_clean() {
    _wt_repo_setup || return 1
    local wtp branch ismain detached bare locked
    local res safety reasons
    local -a safe_items unsafe_lines
    safe_items=(); unsafe_lines=()

    while IFS=$'\t' read -r wtp branch ismain detached bare locked; do
        [ "$ismain" = 1 ] && continue
        [ "$bare" = 1 ] && continue
        res=$(_wt_safety "$wtp" "$branch" "$ismain" "$detached" "$bare")
        safety="${res%%|*}"; reasons="${res#*|}"
        if [ "$safety" = safe ] || [ "$safety" = missing ]; then
            safe_items+=("$wtp"$'\t'"$branch")
        else
            unsafe_lines+=("$(basename "$wtp") [${branch:-(detached)}] - $reasons")
        fi
    done <<EOF
$(_wt_list_records)
EOF

    echo "=== Worktree Cleanup ==="
    echo ""

    if [ "${#unsafe_lines[@]}" -gt 0 ]; then
        echo "Skipping ${#unsafe_lines[@]} unsafe worktree(s):"
        local line
        for line in "${unsafe_lines[@]}"; do
            echo "  ⚠ $line"
        done
        echo ""
    fi

    if [ "${#safe_items[@]}" -eq 0 ]; then
        echo "No safe worktrees to clean up."
        return 0
    fi

    echo "Found ${#safe_items[@]} safe worktree(s) to remove:"
    local item p b
    for item in "${safe_items[@]}"; do
        p="${item%%$'\t'*}"; b="${item#*$'\t'}"
        echo "  ✓ $(basename "$p") [${b:-(detached)}]"
    done
    echo ""

    local ans del_branches
    printf 'Delete all safe worktrees? (y/n): '
    read -r ans
    case "$ans" in y|yes) ;; *) echo "Cancelled."; return 0 ;; esac
    echo ""
    printf 'Also delete their branches? (y/n): '
    read -r del_branches
    echo ""

    local deleted=0 force
    for item in "${safe_items[@]}"; do
        p="${item%%$'\t'*}"; b="${item#*$'\t'}"
        force=""
        [ -d "$p" ] || force="--force"
        if git worktree remove $force "$p" 2>/dev/null; then
            echo "✓ Removed worktree: $(basename "$p")"
            deleted=$((deleted + 1))
            case "$del_branches" in
                y|yes)
                    if [ -n "$b" ] && git branch -d "$b" 2>/dev/null; then
                        echo "  ✓ Deleted branch: $b"
                    fi
                    ;;
            esac
        else
            echo "✗ Failed to remove: $(basename "$p")"
        fi
    done

    echo ""
    echo "Cleaned up $deleted worktree(s)."
}

# --------------------------------------------------------------------------
# wt doctor
# --------------------------------------------------------------------------

_wt_is_function() {
    if [ -n "$ZSH_VERSION" ]; then
        case "$(whence -w wt 2>/dev/null)" in *function) return 0 ;; esac
        return 1
    else
        [ "$(type -t wt 2>/dev/null)" = function ]
    fi
}

_wt_cmd_doctor() {
    echo "wt doctor"
    echo ""

    if _wt_is_function; then
        echo "  ✓ wt is a shell function — in-shell 'cd' works"
    else
        echo "  ✗ wt is NOT a function — source bin/wt.sh from your shell rc so 'cd' works"
    fi

    if command -v fzf >/dev/null 2>&1; then
        echo "  ✓ fzf present ($(fzf --version 2>/dev/null | awk '{print $1}'))"
    else
        echo "  ⚠ fzf missing — pickers fall back to a numbered menu"
    fi

    if command -v gh >/dev/null 2>&1; then
        if gh auth status >/dev/null 2>&1; then
            echo "  ✓ gh present and authenticated — squash-merge detection enabled"
        else
            echo "  ⚠ gh present but not authenticated — run 'gh auth login'"
        fi
    else
        echo "  ⚠ gh missing — squash-merge detection disabled"
    fi

    local f="${WT_CONFIG:-$HOME/.config/wt/config}"
    if [ -r "$f" ]; then
        echo "  ✓ config: $f"
    else
        echo "  – no config file (using defaults): $f"
    fi

    if git rev-parse --git-dir >/dev/null 2>&1; then
        local mb
        mb=$(git symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's@^refs/remotes/origin/@@')
        echo "  ✓ inside a git repository (main branch: ${WT_DEFAULT_BRANCH:-${mb:-main}})"
    else
        echo "  – not currently in a git repository"
    fi
}

# --------------------------------------------------------------------------
# Usage
# --------------------------------------------------------------------------

_wt_usage() {
    cat <<'USAGE'
wt — git worktree manager

Usage:
  wt [switch] [query]   Fuzzy-pick a worktree and cd into it (default command)
                        A query pre-filters the list and goes straight there
                        on a single match.
  wt new <branch> [path]  Create a worktree for a branch (sibling dir), then cd
  wt new -b <branch> [path]  Create a worktree with a brand-new branch
  wt new -b <branch> --from <base>  Branch off <base> rather than current HEAD
  wt ls                 List worktrees with safety status
  wt rm [query]         Remove a worktree (safety-gated), optionally its branch
  wt clean              Batch-remove every "safe" worktree
  wt doctor             Check your setup (fzf, gh, shell integration, config)
  wt help               Show this help

Aliases: switch=cd/sw  new=add/mk  ls=list/l  rm=remove  clean=prune

A worktree is "safe to delete" when it has no uncommitted changes, no unpushed
commits, and its branch is merged into the main branch.

Config (~/.config/wt/config, or $WT_CONFIG), KEY=value lines / env vars:
  WT_BASE_DIR        Where new worktrees go (default: sibling of the main repo)
  WT_DEFAULT_BRANCH  Override the detected main branch (default: origin HEAD/main)
  WT_AUTO_CD         1 = cd after 'new' without prompting (default: prompt)
  WT_USE_FZF         0 = always use the numbered menu instead of fzf
  WT_COPY_UNTRACKED  Space-separated globs copied into a new worktree, e.g.
                     ".env .env.local"  (default: off)
  WT_POST_CREATE     Shell command run inside a newly created worktree
  WT_NO_COLOR / NO_COLOR  Disable colored output
USAGE
}

# --------------------------------------------------------------------------
# Dispatcher
# --------------------------------------------------------------------------

wt() {
    _wt_load_config
    local cmd="$1"
    [ "$#" -gt 0 ] && shift
    case "$cmd" in
        ""|switch|sw|cd)  _wt_cmd_switch "$@" ;;
        new|add|mk)       _wt_cmd_new "$@" ;;
        ls|list|l)        _wt_cmd_ls "$@" ;;
        rm|remove)        _wt_cmd_rm "$@" ;;
        clean|prune)      _wt_cmd_clean "$@" ;;
        doctor)           _wt_cmd_doctor "$@" ;;
        help|-h|--help)   _wt_usage ;;
        *)
            echo "wt: unknown command '$cmd'" >&2
            _wt_usage >&2
            return 1
            ;;
    esac
}

# --------------------------------------------------------------------------
# Completion sources
# --------------------------------------------------------------------------

_wt_branches() {
    git for-each-ref --format='%(refname:short)' refs/heads refs/remotes/origin 2>/dev/null \
        | sed 's@^origin/@@' | grep -vE '^(HEAD|origin)$' | sort -u
}

_wt_worktree_names() {
    _wt_list_records 2>/dev/null | cut -f1 | while IFS= read -r p; do
        [ -n "$p" ] && basename "$p"
    done
}

# --------------------------------------------------------------------------
# Completion registration (zsh + bash)
# --------------------------------------------------------------------------

if [ -n "${ZSH_VERSION:-}" ]; then
    _wt_complete_zsh() {
        local -a cmds
        cmds=(switch new ls rm clean doctor help)
        if (( CURRENT == 2 )); then
            compadd -- "${cmds[@]}"
        else
            case "${words[2]}" in
                new|add|mk)
                    local -a brs
                    brs=("${(@f)$(_wt_branches)}")
                    compadd -- "${brs[@]}"
                    ;;
                switch|sw|cd|rm|remove)
                    local -a wts
                    wts=("${(@f)$(_wt_worktree_names)}")
                    compadd -- "${wts[@]}"
                    ;;
            esac
        fi
    }
    # Ensure compdef is available (compinit may not have run yet).
    if ! whence compdef >/dev/null 2>&1; then
        autoload -Uz compinit && compinit -C 2>/dev/null
    fi
    compdef _wt_complete_zsh wt 2>/dev/null
elif [ -n "${BASH_VERSION:-}" ]; then
    _wt_complete_bash() {
        local cur
        cur="${COMP_WORDS[COMP_CWORD]}"
        COMPREPLY=()
        if [ "$COMP_CWORD" -eq 1 ]; then
            COMPREPLY=( $(compgen -W "switch new ls rm clean doctor help" -- "$cur") )
        else
            case "${COMP_WORDS[1]}" in
                new|add|mk)
                    COMPREPLY=( $(compgen -W "$(_wt_branches)" -- "$cur") )
                    ;;
                switch|sw|cd|rm|remove)
                    COMPREPLY=( $(compgen -W "$(_wt_worktree_names)" -- "$cur") )
                    ;;
            esac
        fi
    }
    complete -F _wt_complete_bash wt
fi
