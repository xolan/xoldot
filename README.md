# xoldot

`xoldot` manages tools, shell aliases, agent skills, and files linked into your
home directory. Its configuration lives in `~/.config/xoldot` by default and
can be synchronized through Git.

## Table of contents

- [Get started](#get-started)
- [Apply](#apply)
- [Inspect before applying](#inspect-before-applying)
- [Tools](#tools)
- [Agent skills](#agent-skills)
- [Aliases](#aliases)
- [Managed home files](#managed-home-files)
- [Sync](#sync)
- [Shell completion](#shell-completion)
- [Overrides and limits](#overrides-and-limits)
- [Development](#development)

## Get started

Run the interactive setup:

```sh
xoldot setup
```

Setup creates any missing files in this layout:

```text
~/.config/xoldot/
├── xoldot.toml
├── profiles/
├── skills.toml
├── files/
│   ├── aliases.toml
│   └── home/
└── tools.toml
```

The `profiles/` directory is reserved for future use. Profiles are not
implemented yet.

Enter a Git remote URL when prompted to enable sync. Leave it blank to keep
Git disabled. If the remote already has a `main` branch, setup checks it out
before creating the missing files.

Apply the configuration to the current machine:

```sh
xoldot apply
```

This runs every Apply part. See [Apply](#apply) to run only selected parts.

The default configuration is:

```toml
[git]
enabled = false
remote = "origin"
branch = "main"

[aliases]
dir = "~/.aliases"
shells = ["bash", "zsh", "fish"]
```

Use `--verbose` or `-v` to print the Git and `npx` commands that xoldot runs.

## Apply

With no `--only` flag, Apply runs all three parts in this order: `tools`,
`managed-home`, then `aliases`. Select one part or repeat the flag to select
more than one:

```sh
xoldot apply --only managed-home
xoldot apply --only managed-home --only aliases
xoldot apply --only tools --dry
```

The accepted values are exactly `tools`, `managed-home`, and `aliases`.
Repeated values count once. Selected parts keep the default order, regardless
of flag order. Beyond loading `xoldot.toml`, Apply reads, validates, changes,
and reports only the selected parts. `--dry` previews the same selection.

## Inspect before applying

Inspect the current Machine without changing it:

```sh
xoldot status
```

Status lists each managed home link as current, missing, stale, or conflicting.
It reports the Alias output as current, missing, safely replaceable, or
conflicting. It also verifies installed Skill digests and ownership using local
files.

Status counts declared Tools as unchecked. It does not run their `check`
commands because those commands are user-authored shell code. It also does not
run install commands, `npx`, Git operations, or lifecycle scripts.

Show only the pending managed home and Alias work:

```sh
xoldot diff
```

Diff prints the link additions and removals that a dry Apply would plan. For an
owned Alias file that needs replacement, it prints a unified text diff. Missing
Alias output is reported as a planned creation. Content that xoldot cannot
safely replace is reported as a conflict.

Both commands are read-only. They do not create the Target home, state
directories, or output files. Drift and conflicts are successful inspection
results. Invalid configuration, unreadable paths, and invalid ownership state
still return an error.

## Tools

Add a tool, then define how to detect and install it in `tools.toml`:

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

[[tool]]
name = "jq"
check = "command -v jq"

[tool.install]
macos = "brew install jq"

[tool.install.linux]
default = "sudo apt install jq"
arch = "yay -S jq"
```

On Linux, distribution IDs such as `arch` override `default`. xoldot reads the
ID from `/etc/os-release`.

When Apply includes `tools`, xoldot runs `check` with `/bin/sh`. If it fails,
xoldot runs the matching install command and checks again. Install commands are
trusted shell commands and may prompt for credentials.

```sh
xoldot tool list
xoldot tool remove ripgrep
```

## Agent skills

Add a skill from GitHub with the shorthand form:

```sh
xoldot skill add unslop@poteto/plugins
```

Or provide a Git URL or filesystem path:

```sh
xoldot skill add unslop --from https://github.com/poteto/plugins
xoldot skill add local-skill --from ./skills/local-skill
```

Manage installed skills with:

```sh
xoldot skill list
xoldot skill update unslop
xoldot skill update
xoldot skill remove unslop
```

Skill commands require Node.js 22.20 or newer and `npx`. xoldot stores skills
under `files/home/.agents/skills`. Apply links them into `~/.agents/skills` and
`~/.claude/skills`. Run `xoldot apply` after changing skills.

If the selected skill belongs to a plugin with an `agents` directory, xoldot
also installs the Markdown agent definitions that the skill names from the
nearest such directory. Under `files/home/.agents`, it uses the same sibling
layout as the plugin: `skills/<skill>` and `agents/<agent>.md`. Agent ownership
is recorded in `skills.toml`. Apply links companion agents into
`~/.agents/agents` and `~/.claude/agents`. Git-backed sources require `git` for
companion-agent discovery. Xoldot does not convert Markdown agent definitions
into Codex TOML agent configurations.

xoldot refuses to update or remove a skill or companion agent with local
changes. It also refuses to overwrite an agent owned by another skill or by the
user. Relative paths are saved as absolute paths. Git URLs cannot contain
embedded credentials or query parameters, so use a Git credential helper for
authentication.

`xoldot status` checks installed Skill content, companion agents, and Claude
compatibility links against the ownership data in `skills.toml`. This check is
local and does not fetch a Skill source.

## Aliases

Add or update an alias:

```sh
xoldot alias add ll "ls -la"
xoldot alias add gs "git status"
```

Aliases are stored in `files/aliases.toml`. When Apply includes `aliases`, it
detects the current shell from `SHELL` and renders one of these files:

```text
~/.aliases/alias.bash
~/.aliases/alias.zsh
~/.aliases/alias.fish
```

Source the matching file from your shell configuration. For Bash:

```sh
# ~/.bashrc
source ~/.aliases/alias.bash
```

Set `XOLDOT_SHELL` to `bash`, `zsh`, or `fish` to override shell detection.
xoldot will not overwrite an alias file that it did not create or one that has
local changes.

## Managed home files

Every file below `files/home` maps to the same path below your home directory:

```text
~/.config/xoldot/files/home/.config/git/config
                         -> ~/.config/git/config
```

To start managing an existing file, adopt it:

```sh
xoldot adopt ~/.config/git/config
```

Adopt stages the file at the matching path below `files/home`, checks its bytes
and permission bits, and then replaces the original with the same link that
apply would create. Staging uses a copy, so a Configuration directory on a
different filesystem is supported. The source stays unchanged until the copy
has been checked. Run `xoldot setup` first so the managed home directory exists.

Adopt serializes ownership-state updates. If another xoldot process changes the
state after a command plans its work, the stale command stops before moving its
source. A later failure restores paths only when they are still the exact files,
directories, or links created by that command. It preserves unexpected
replacements and reports the backup that still contains the original file.

The first release adopts one ordinary file at a time. It refuses directories,
symlinks, special files, paths outside the Target home, paths inside the
Configuration directory, and any path whose managed destination already
exists. It preserves file bytes and permission bits, but not extended
attributes or other platform-specific metadata. Adopt does not apply Tools or
Aliases and does not sync the Configuration repository.

xoldot creates parent directories when it links managed files. It will not
replace an existing file or a symlink that points somewhere else. If you remove
a managed file, the next Apply that includes `managed-home` removes its old
home link only when that link still points to the managed location.

## Sync

```sh
xoldot sync
```

Sync commits local changes as `xoldot sync`, pulls the configured branch with
rebase, and pushes it. Git provides the author identity and remote
authentication.

Preview inspection, adoption, Apply, or Sync without changing anything:

```sh
xoldot status
xoldot diff
xoldot adopt ~/.config/git/config --dry
xoldot apply --dry
xoldot sync --dry
```

Dry adoption prints the exact move and link. Dry Apply does not run selected
Tool checks because they are user-authored shell commands. Unlike `xoldot diff`,
dry Apply also describes what each selected Tool check and installation would
do.

## Shell completion

`xoldot completion` prints a completion script to standard output. Install the
script for your shell in its normal user completion directory:

```sh
# Bash
mkdir -p ~/.local/share/bash-completion/completions
xoldot completion bash > ~/.local/share/bash-completion/completions/xoldot

# Zsh
mkdir -p ~/.zfunc
xoldot completion zsh > ~/.zfunc/_xoldot
# Add ~/.zfunc to fpath before calling compinit in ~/.zshrc.

# Fish
mkdir -p ~/.config/fish/completions
xoldot completion fish > ~/.config/fish/completions/xoldot.fish
```

Generation does not create or change files. Redirect the output when you want
to install it.

## Overrides and limits

- `--config-dir DIR` or `XOLDOT_CONFIG_HOME` changes the configuration
  directory.
- `NO_COLOR` or `TERM=dumb` disables color.

Conflict backup or force modes and shells other than Bash, Zsh, and Fish are
not supported yet.

## Development

[mise](https://mise.jdx.dev/) manages the development tools and commands:

```sh
mise trust
mise install
mise run check
mise run install
```

Use the mise tasks instead of invoking `go`, `gofmt`, or `golangci-lint`
directly. Run `mise tasks` to list them. Set `XOLDOT_TARGET_HOME` to test apply
against an isolated home directory.
