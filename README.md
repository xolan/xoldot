# xoldot

`xoldot` is a small dotfiles manager for tools, shell aliases, and files linked
into your home directory. Its state lives in `~/.config/xoldot` by default and
can be synchronized through a Git remote.

## Development

[mise](https://mise.jdx.dev/) owns the Go and lint tool versions as well as all
development commands:

```sh
mise trust
mise install
mise run check
mise run install
```

Do not invoke `go`, `gofmt`, or `golangci-lint` directly in this repository.
Available tasks are:

| Task | Purpose |
| --- | --- |
| `mise run tidy` | Update module metadata |
| `mise run fmt` | Format Go code |
| `mise run fmt-check` | Verify formatting without changing files |
| `mise run lint` | Run golangci-lint |
| `mise run actions-lint` | Validate GitHub Actions workflows |
| `mise run test` | Run tests |
| `mise run build` | Build `dist/xoldot` |
| `mise run run -- <args>` | Run xoldot from source |
| `mise run package` | Build `dist/xoldot-$VERSION` |
| `mise run archive` | Package the versioned binary as a tar archive |
| `mise run install` | Install through `go install` |
| `mise run check` | Run formatting, lint, tests, and build gates |

The GitHub Actions CI pipeline runs formatting, Go lint, and workflow lint in
parallel. Linux and macOS tests start after those checks pass, followed by
parallel platform builds and uploaded artifacts.

## Getting started

Run the interactive setup:

```sh
xoldot setup
```

Setup creates this layout without overwriting files that already exist:

```text
~/.config/xoldot/
├── xoldot.toml
├── profiles/
├── skills.toml
├── files/
│   ├── aliases.toml
│   └── home/
├── tools.toml
└── bootstrap.sh
```

Git is disabled initially. Supplying a remote URL during setup initializes the
configuration directory as a repository, adds it as `origin`, and enables
`xoldot sync`. On a fresh machine, an existing remote branch is checked out
before missing layout files are initialized. A blank URL leaves Git disabled.
The generated `bootstrap.sh` runs `xoldot apply` on a machine where xoldot is
already installed.

The top-level defaults are:

```toml
[[git]]
enabled = false
remote = "origin"
branch = "main"

[[aliases]]
dir = "~/.aliases"
shells = ["bash", "zsh", "fish"]
```

For testing or unusual layouts, `--config-dir DIR` overrides the configuration
directory. `XOLDOT_CONFIG_HOME` provides the equivalent environment override.

## Tools

Add a tool, then fill in its install commands in `tools.toml`:

```sh
xoldot tool add ripgrep
```

```toml
[[tool]]
name = "ripgrep"
check = "command -v rg"

[tool.install]
macos = "brew install ripgrep"

[tool.install.linux]
default = "sudo apt install ripgrep"
arch = "yay -S ripgrep"
```

TOML cannot make `install.linux` both a string and a table, so `default` is the
Debian-style base Linux command and distribution IDs such as `arch` override
it. xoldot reads `ID` and `ID_LIKE` from `/etc/os-release` and falls back to
`default` when no override exists.

`xoldot apply` runs each `check` with `/bin/sh`. When the check fails, it runs
the matching install command and verifies the check again. These are trusted,
user-authored shell commands and may prompt for credentials through tools such
as `sudo`.

Remove an entry with:

```sh
xoldot tool remove ripgrep
```

## Agent skills

Skill commands manage global, home-folder agent skills. The shorthand form
assumes a GitHub repository:

```sh
xoldot skill add unslop@poteto/noodle
```

An explicit source is also accepted:

```sh
xoldot skill add unslop --from https://github.com/poteto/noodle
xoldot skill update unslop       # update one skill
xoldot skills update             # update all; "skills" is an alias
xoldot skill remove unslop
```

These commands require Node.js 22.20 or newer and `npx`. Development uses the
Node version pinned by mise. xoldot delegates fetching to the pinned
`skills@1.5.23` npm package, but redirects its global home into the managed
tree. Canonical files therefore land in
`files/home/.agents/skills/<skill>`. `skills.toml` records each source and a
content digest; update and remove refuse to proceed if the canonical skill has
local changes.

For Claude compatibility, xoldot creates an ordinary directory hierarchy at
`files/home/.claude/skills/<skill>` and a relative symlink for each individual
skill file. It never creates a directory symlink. Run `xoldot apply` after an
add, update, or removal to reconcile the corresponding links in your real
home directory.

## Aliases

Add or update an alias with:

```sh
xoldot alias add ll "ls -la"
```

Aliases are stored as data in `files/aliases.toml`. During `xoldot apply`, the
current shell is detected from `SHELL` and one of these files is generated:

```text
~/.aliases/alias.bash
~/.aliases/alias.zsh
~/.aliases/alias.fish
```

Only the detected shell's file is rendered. Source that file from the matching
shell startup file, for example:

```sh
# ~/.bashrc
source ~/.aliases/alias.bash
```

`XOLDOT_SHELL=bash|zsh|fish` can override detection when needed.

## Managed home files

Every file below `files/home` maps to the same relative path below the current
user's home. For example:

```text
~/.config/xoldot/files/home/.config/git/config
                         -> ~/.config/git/config
```

To adopt an existing file safely, move it into the managed tree before apply:

```sh
mkdir -p ~/.config/xoldot/files/home/.config/git
mv ~/.config/git/config ~/.config/xoldot/files/home/.config/git/config
xoldot apply
```

Apply creates parent directories and links each file individually. Ordinary
managed files use absolute symlinks; managed relative file symlinks, such as
the Claude compatibility links, stay relative after being mapped into the
target home. Apply is idempotent and accepts an existing symlink only when its
destination exactly matches the link xoldot would create. It refuses to
overwrite ordinary files or any mismatched symlink.

Applied links are recorded in `~/.local/state/xoldot/links.json`. If a managed
file is removed, a later apply removes the stale home link only when its exact
destination still matches that record. User-created or changed files and
symlinks are left untouched.

`XOLDOT_TARGET_HOME` overrides the destination home for isolated testing.

## Sync

```sh
xoldot sync
```

Sync stages the configuration tree, creates an `xoldot sync` commit when local
changes exist, pulls the configured branch with rebase when it already exists
on the remote, and pushes the result. Git's normal `user.name`, `user.email`,
and remote authentication configuration are used.

## Current scope

The `profiles/` directory is reserved for profile inheritance, but profiles are
not interpreted in this first release. Conflict backup/force modes and shells
other than Bash, Zsh, and Fish are also intentionally outside the current
scope.
