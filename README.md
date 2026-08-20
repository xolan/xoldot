# xoldot

`xoldot` manages tools, shell aliases, agent skills, and files linked into your
home directory. Its configuration lives in `~/.config/xoldot` by default and
can be synchronized through Git.

## Table of contents

- [Get started](#get-started)
- [Tools](#tools)
- [Agent skills](#agent-skills)
- [Aliases](#aliases)
- [Managed home files](#managed-home-files)
- [Sync](#sync)
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

Enter a Git remote URL when prompted to enable sync. Leave it blank to keep
Git disabled. If the remote already has a `main` branch, setup checks it out
before creating the missing files.

Apply the configuration to the current machine:

```sh
xoldot apply
```

This installs missing tools, links managed home content, and renders aliases
for the current shell.

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

During apply, xoldot runs `check` with `/bin/sh`. If it fails, xoldot runs the
matching install command and checks again. Install commands are trusted shell
commands and may prompt for credentials.

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

## Aliases

Add or update an alias:

```sh
xoldot alias add ll "ls -la"
xoldot alias add gs "git status"
```

Aliases are stored in `files/aliases.toml`. Apply detects the current shell
from `SHELL` and renders one of these files:

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

To start managing an existing file, move it into the configuration tree and
apply:

```sh
mkdir -p ~/.config/xoldot/files/home/.config/git
mv ~/.config/git/config ~/.config/xoldot/files/home/.config/git/config
xoldot apply
```

xoldot creates the parent directories and symlinks the file. It will not
replace an existing file or a symlink that points somewhere else. If you
remove a managed file, the next apply removes its old home link only when that
link still points to the managed location.

## Sync

```sh
xoldot sync
```

Sync commits local changes as `xoldot sync`, pulls the configured branch with
rebase, and pushes it. Git provides the author identity and remote
authentication.

Preview an apply or sync without changing anything:

```sh
xoldot apply --dry
xoldot sync --dry
```

Dry apply does not run tool checks because they are user-authored shell
commands.

## Overrides and limits

- `--config-dir DIR` or `XOLDOT_CONFIG_HOME` changes the configuration
  directory.
- `NO_COLOR` or `TERM=dumb` disables color.

Profiles, conflict backup or force modes, and shells other than Bash, Zsh, and
Fish are not supported yet.

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
