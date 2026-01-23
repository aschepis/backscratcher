# Developer Productivity Toolkit

A collection of shell scripts, git aliases, and CLI tools for a faster development workflow.

## Git Worktree Scripts

Scripts for managing git worktrees efficiently. All scripts are interactive and use `select` menus for easy navigation.

| Command | Description |
|---------|-------------|
| `cdwt` | Switch to a worktree interactively |
| `lswt` | List all worktrees with safety status |
| `rmwt` | Delete a worktree with safety checks |
| `mkwt` | Create a worktree from branch or PR |
| `cleanwt` | Batch delete all "safe" worktrees |

### cdwt

Switch to a git worktree using an interactive menu.

```bash
$ cdwt
1) /Users/you/project
2) /Users/you/project-feature-x
3) /Users/you/project-bugfix-y
Select a worktree to switch to: 2
```

### lswt

List all worktrees with their deletion safety status. A worktree is "safe" when:
- No uncommitted changes
- No unpushed commits
- Branch is merged into main

```bash
$ lswt
WORKTREE                                           BRANCH               STATUS
==========================================================================================
/Users/you/project                                 main                 (main worktree)
/Users/you/project-feature-x                       feature-x            ✓ safe to delete
/Users/you/project-wip                             wip-branch           ⚠ uncommitted changes
```

### rmwt

Interactively delete a worktree with safety checks. Requires explicit "yes" confirmation for unsafe worktrees. Optionally deletes the associated branch.

```bash
$ rmwt
Select a worktree to delete:

1) feature-x [feature-x] ✓
2) wip [wip-branch] ⚠ uncommitted changes
Selection (or 'q' to quit): 1

Worktree: /Users/you/project-feature-x
Branch:   feature-x
Status:   ✓ safe to delete

Delete this worktree? (y/n): y

Removing worktree...
✓ Worktree removed.
Also delete branch 'feature-x'? (y/n): y
✓ Branch deleted.
```

### mkwt

Create a new worktree from a branch name or GitHub PR number.

```bash
# From existing branch
mkwt feature-auth

# Create new branch
mkwt -b new-feature

# From GitHub PR (requires gh CLI)
mkwt 1234
```

Worktrees are created as siblings to the main repo directory:
```
~/src/
├── my-project/           # main worktree
├── my-project-feature-x/ # created by: mkwt feature-x
└── my-project-pr-1234/   # created by: mkwt 1234
```

### cleanwt

Batch delete all worktrees that are safe to remove. Shows unsafe worktrees that will be skipped, then prompts for confirmation.

```bash
$ cleanwt
=== Worktree Cleanup ===

Skipping 1 unsafe worktree(s):
  ⚠ wip [wip-branch] - uncommitted changes

Found 2 safe worktree(s) to remove:
  ✓ feature-x [feature-x]
  ✓ bugfix-y [bugfix-y]

Delete all safe worktrees? (y/n): y

Also delete their branches? (y/n): y

✓ Removed worktree: feature-x
  ✓ Deleted branch: feature-x
✓ Removed worktree: bugfix-y
  ✓ Deleted branch: bugfix-y

Cleaned up 2 worktree(s).
```

## Git Aliases

### Pre-existing

```bash
git co          # checkout
git st          # status
git lg          # pretty log graph
git ri          # rebase -i
```

### Added

```bash
# Quick status
git staged      # Show staged changes (diff --cached)
git recent      # List branches by most recent commit

# Branch workflow
git cb <name>   # Create and checkout new branch
git main        # Checkout main and pull
git cleanup     # Delete all merged branches

# Undo/recovery
git uncommit    # Undo last commit, keep changes staged
git nevermind   # Discard all local changes (checkout -- .)
```

### Installation

Add these to your global git config:

```bash
git config --global alias.staged 'diff --cached'
git config --global alias.recent 'branch --sort=-committerdate --format="%(committerdate:relative)%09%(refname:short)"'
git config --global alias.cb 'checkout -b'
git config --global alias.main '!git checkout main && git pull'
git config --global alias.cleanup '!git branch --merged | grep -v "\\*\\|main\\|master" | xargs -n 1 git branch -d'
git config --global alias.uncommit 'reset --soft HEAD~1'
git config --global alias.nevermind 'checkout -- .'
```

## Shell Enhancements

### fzf (Fuzzy Finder)

A general-purpose fuzzy finder that integrates with the shell.

**Installation:**
```bash
brew install fzf
```

**Keybindings (added to .zshrc):**

| Key | Action |
|-----|--------|
| `Ctrl+R` | Fuzzy search command history |
| `Ctrl+T` | Fuzzy find files, insert path at cursor |
| `Alt+C` | Fuzzy cd into subdirectory |

**Configuration (.zshrc):**
```bash
source <(fzf --zsh)
export FZF_DEFAULT_OPTS='--height 40% --layout=reverse --border'
```

**Custom functions:**
```bash
# Fuzzy git branch checkout
gcof() {
  git checkout $(git branch --all | fzf --prompt="checkout> " | sed 's/remotes\/origin\///' | xargs)
}

# Fuzzy git log browser with preview
glogf() {
  git log --oneline --graph --decorate | fzf --preview 'git show --color=always {+1}' --no-sort
}
```

### zoxide (Smarter cd)

A smarter `cd` command that learns your habits and lets you jump to directories by partial name.

**Installation:**
```bash
brew install zoxide
```

**Configuration (.zshrc):**
```bash
eval "$(zoxide init zsh)"
```

**Usage:**
```bash
z proj          # Jump to most frequently used dir matching "proj"
z foo bar       # Jump to dir matching both "foo" and "bar"
zi              # Interactive mode with fzf
```

zoxide ranks directories by "frecency" (frequency + recency). The more you visit a directory, the higher it ranks.

## Setup

1. Clone this repo and add the `bin/` directory to your PATH:
   ```bash
   export PATH="$PATH:/path/to/this/repo/bin"
   ```

2. Install dependencies:
   ```bash
   brew install fzf zoxide
   ```

3. Add shell configuration to your `.zshrc`:
   ```bash
   # fzf
   source <(fzf --zsh)
   export FZF_DEFAULT_OPTS='--height 40% --layout=reverse --border'

   # zoxide
   eval "$(zoxide init zsh)"
   ```

4. Restart your shell or run `source ~/.zshrc`

## Dependencies

- `git` - required for all worktree scripts
- `gh` - GitHub CLI, optional, enables `mkwt <PR-number>`
- `fzf` - fuzzy finder
- `zoxide` - smarter cd
- `jq` - JSON parsing, used by mkwt for PR info
