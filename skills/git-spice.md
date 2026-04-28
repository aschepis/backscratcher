---
name: git-spice
description: Use when the user asks about git-spice, stacked branches, stacked PRs, or managing a stack of branches with gs/git-spice. Covers setup, daily workflow, submitting PRs, restacking, and advanced operations.
---

# git-spice

git-spice (`gs`) is a CLI tool for managing stacked Git branches — a workflow where multiple branches build on top of each other so each can be reviewed and merged independently.

**Install:** `brew install git-spice` then add `alias gs=git-spice` to your shell config.

---

## Core Concepts

- **Trunk** — the default branch (main/master). Has no base.
- **Stack** — a chain of branches, each built on top of the previous.
- **Base** — the branch a given branch was created from.
- **Upstack** — branches above the current one (depend on it).
- **Downstack** — branches below the current one (it depends on them).
- **Restacking** — rebasing a branch onto its (possibly updated) base to keep history linear.

```
trunk
└── feat/auth          ← downstack from feat/ui
    └── feat/ui        ← current branch
        └── feat/tests ← upstack from feat/ui
```

---

## Setup

```bash
# One-time per repo (optional — gs auto-initializes when needed)
gs repo init
```

---

## Daily Workflow

### Creating a stack

```bash
# Start on trunk, make changes, create first branch
git checkout main
# ... make changes ...
gs branch create feat/auth           # stages + commits + creates branch
                                     # -a to auto-stage tracked files
                                     # -m "msg" to set commit message inline

# Stack a second branch on top
# ... make more changes ...
gs branch create feat/ui             # base is automatically feat/auth

# Stack a third
gs branch create feat/tests
```

`gs branch create` stages your changes, creates a commit, and registers the branch — all in one step.

### Navigating the stack

```bash
gs up          # move one branch up (toward trunk tip)
gs down        # move one branch down (toward trunk)
gs top         # jump to the top of the stack
gs bottom      # jump to the bottom
gs trunk       # jump to trunk
gs branch checkout <name>   # jump to any tracked branch (with fuzzy prompt)
```

### Viewing the stack

```bash
gs log short   # compact stack view (current stack only)
gs log short -a  # all tracked branches
gs log long    # stack with commits listed
```

### Committing changes

Prefer `gs commit create` over `git commit` — it auto-restacks upstack branches after committing:

```bash
gs commit create -m "fix: handle null case"   # commit + restack upstack
gs commit amend                                # amend HEAD + restack upstack
```

You can still use `git commit` for simple cases where you'll restack manually.

---

## Restacking

When a branch's base changes (e.g. after amending a downstack commit), upstack branches diverge and need to be rebased.

```bash
gs upstack restack      # restack current branch + everything above it
gs stack restack        # restack the entire stack
gs branch restack       # restack only the current branch
```

`gs commit create` and `gs commit amend` call `gs upstack restack` automatically.

If a rebase conflict occurs, resolve it then run:
```bash
gs rebase continue   # continue after resolving conflicts
gs rebase abort      # abort and return to pre-restack state
```

---

## Tracking an Existing Branch

If you created a branch with `git checkout -b` instead of `gs branch create`:

```bash
gs branch track          # track current branch (prompts for base)
gs downstack track       # track all untracked branches below current
```

---

## Submitting Pull Requests

```bash
gs branch submit         # create/update PR for current branch only
gs stack submit          # create/update PRs for entire stack
gs upstack submit        # create/update PRs for current + above
gs downstack submit      # create/update PRs for current + below
```

Useful flags:
```bash
gs stack submit --fill        # populate title/body from commit messages (skip prompt)
gs stack submit --draft       # mark all as draft
gs stack submit --no-draft    # un-draft all
gs stack submit --dry-run     # preview without submitting
gs stack submit -w            # open submitted PRs in browser
```

git-spice automatically sets the PR base branch to match the stack — when feat/auth merges, feat/ui's base is updated to main automatically.

PRs get a navigation comment showing where they sit in the stack.

---

## Syncing After Merges

When one or more PRs in the stack have been merged:

```bash
gs repo sync       # pull latest trunk, delete merged branches, rebase survivors
```

This is the equivalent of "my PR merged, now clean up." After syncing, run `gs branch submit` on the next branch to update its base in GitHub.

---

## Advanced Operations

### Move a branch onto a different base

```bash
gs upstack onto main       # detach current branch (+ upstack) and rebase onto main
gs branch onto feat/other  # move only current branch, leave upstack on old base
```

### Fold a branch into its base

```bash
gs branch fold    # squash current branch's commits into its base and delete the branch
```

Useful for collapsing a fixup branch you no longer want to keep separate.

### Fixup an older commit in the stack

```bash
gs commit fixup   # interactively pick a commit below HEAD to fixup into
```

### Reorder branches in a stack

```bash
gs stack edit     # opens $EDITOR to reorder branches (like interactive rebase for stacks)
```

### Split a branch

```bash
gs branch split   # interactively split current branch into two at a commit boundary
gs commit split   # split the current HEAD commit into two
```

### Squash a branch

```bash
gs branch squash  # squash all commits on current branch into one
```

---

## Key Shorthands

All commands have short aliases:

| Full | Short |
|------|-------|
| `gs branch create` | `gs bc` |
| `gs branch checkout` | `gs bco` |
| `gs branch submit` | `gs bs` |
| `gs stack submit` | `gs ss` |
| `gs upstack restack` | `gs usr` |
| `gs stack restack` | `gs sr` |
| `gs repo sync` | `gs rs` |
| `gs log short` | `gs ls` |
| `gs up` | `gs u` |
| `gs down` | `gs d` |

---

## Typical End-to-End Example

```bash
# Build the stack
git checkout main
gs bc feat/auth -m "add auth middleware"
gs bc feat/endpoints -m "add user endpoints"
gs bc feat/tests -m "add integration tests"

# Submit all as draft PRs
gs stack submit --draft --fill

# Reviewer asks for changes on feat/auth
gs down        # or: gs branch checkout feat/auth
# ... make changes ...
gs commit amend                    # amends + restacks feat/endpoints + feat/tests
gs stack submit                    # updates all 3 PRs

# feat/auth merges
gs repo sync                       # deletes feat/auth, rebases remainder onto main
gs branch submit                   # update feat/endpoints PR base to main
```
