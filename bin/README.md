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

## GitHub Scripts

### gh-pr-unresolved

List all unresolved review threads on the current PR. Must be run from a repo with an open PR checked out.

Output is JSON — one object per unresolved thread with file path, line, author, comment count, the latest comment body, and creation timestamp.

```bash
$ gh-pr-unresolved
[
  {
    "path": "pkg/auth/login.go",
    "line": 42,
    "author": "reviewer",
    "comment_count": 2,
    "latest_comment": "Still needs a nil check here.",
    "created_at": "2024-01-15T10:23:00Z"
  }
]
```

**Dependencies:** `gh` CLI, `jq`

## Go Test Scripts

### go-test-files.sh

Run tests for one or more Go files. Accepts source or test files — if you pass a non-test file it finds the matching `_test.go` automatically.

```bash
go-test-files.sh pkg/auth/login.go pkg/store/user.go
go-test-files.sh pkg/auth/login_test.go

# Filter out JSON status lines from verbose output
go-test-files.sh --clean pkg/auth/login.go
```

### go-test-changes.sh

Run tests for all Go test files that have uncommitted changes (staged or unstaged).

```bash
go-test-changes.sh

# Filter out JSON status lines from verbose output
go-test-changes.sh --clean
```

### clean-test-output.sh

A pipe filter that strips noise from `go test -v` output, leaving only test results. Filters:

- `go test` JSON status lines
- JSON log output (zap, logrus, zerolog, slog)
- logrus text format (`time="..."`, `level=...`)
- zap console format (ISO timestamp + level)
- stdlib log / slog text format (`YYYY/MM/DD HH:MM:SS`)

```bash
go test -v ./... | clean-test-output.sh
```

## AI Tools

### jqai

Like `jq`, but you describe what you want in plain English. Uses Claude to generate the jq expression and runs it for you.

```bash
jqai 'get all user names' data.json
cat data.json | jqai 'extract ids where status is active'
```

**Dependencies:** `claude` CLI, `jq`

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

## Setup

1. Clone this repo and add the `bin/` directory to your PATH:
   ```bash
   export PATH="$PATH:/path/to/this/repo/bin"
   ```

2. Restart your shell or run `source ~/.zshrc`

## Dependencies

- `git` - required for all worktree scripts
- `gh` - GitHub CLI, optional, enables `mkwt <PR-number>` and `gh-pr-unresolved`
- `jq` - JSON parsing, used by `mkwt` and `gh-pr-unresolved`
- `claude` - Claude Code CLI, required by `jqai`
