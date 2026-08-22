# xoldot

`xoldot` manages tools, shell aliases, agent skills, lifecycle scripts, and
files linked into your home directory. Its configuration lives in
`~/.config/xoldot` by default and can be synchronized through Git.

## Table of contents

- [Install](#install)
- [Get started](#get-started)
- [Output](#output)
- [Apply](#apply)
- [Lifecycle scripts](#lifecycle-scripts)
- [Profiles](#profiles)
- [Inspect before applying](#inspect-before-applying)
- [Troubleshoot](#troubleshoot)
- [Tools](#tools)
- [Agent skills](#agent-skills)
- [Aliases](#aliases)
- [Managed home files](#managed-home-files)
- [Sync](#sync)
- [Shell completion](#shell-completion)
- [Overrides and limits](#overrides-and-limits)

## Install

Download the archive for your operating system and architecture from
[GitHub Releases](https://github.com/xolan/xoldot/releases). Releases provide
Linux and macOS binaries for `amd64` and `arm64`, plus a `SHA256SUMS` file.

After checking the archive digest against `SHA256SUMS`, extract and install the
binary. Replace the example version and target with the archive you downloaded:

```sh
version=v0.1.0
os=linux
arch=amd64
archive="xoldot-${version}-${os}-${arch}.tar.gz"
tar -xzf "$archive"
mkdir -p ~/.local/bin
install -m 0755 "xoldot-${version}-${os}-${arch}" ~/.local/bin/xoldot
```

Make sure `~/.local/bin` is on `PATH`, then check the installed version:

```sh
xoldot version
```

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
├── scripts/
│   ├── before-apply/
│   └── after-apply/
└── tools.toml
```

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

## Output

xoldot uses stable prefixes for its own messages:

- `›` for work in progress and dry-run plans
- `✓` for completed work
- `!` for warnings
- `✗` for command errors on standard error
- `+` for underlying Git and `npx` commands shown by `--verbose`

When the destination is a terminal, xoldot colors those prefixes. Completed
work, warnings, command errors, and verbose commands also color the message.
Doctor colors the matching error, warning, or progress symbol for every finding
without repeating the severity as a label. Status also colors state words such
as `current` and `conflict`. Diff colors plan keywords and unified-diff lines.
`NO_COLOR` or `TERM=dumb` disables ANSI styling without removing the text
prefixes.

Output from Git, `npx`, lifecycle scripts, and Tool installers passes through
unchanged. Commands that return data, including `version`, `tool list`, `skill
list`, and `completion`, stay prefix-free so their output can be redirected or
parsed.

## Apply

With no `--only` flag, Apply runs all three parts in this order: `tools`,
`managed-home`, then `aliases`. Select one part or repeat the flag to select
more than one:

```sh
xoldot apply --only managed-home
xoldot apply --only managed-home --only aliases
xoldot apply --only tools --dry
xoldot apply --backup --only managed-home
```

The accepted values are exactly `tools`, `managed-home`, and `aliases`.
Repeated values count once. Selected parts keep the default order, regardless
of flag order. Beyond loading `xoldot.toml`, Apply reads, validates, changes,
and reports only the selected parts when no Profile is selected. `--dry`
previews the same selection.

Lifecycle scripts wrap the selected parts. Eligible before scripts run after
every selected part has prepared successfully, then the selected parts run in
their normal order, followed by eligible after scripts. An Apply with no
lifecycle scripts behaves as before. Tool check commands run after the before
scripts, immediately before Tool installation decisions.

## Lifecycle scripts

Put executable files in `scripts/before-apply/` or `scripts/after-apply/`.
The filename selects when xoldot runs each script:

- `run_` runs on every Apply.
- `run_once_` runs until that relative script path succeeds once.
- `run_onchange_` runs when its content digest differs from its last successful
  run.

Within each phase, xoldot runs eligible scripts in bytewise filename order.
It executes each file directly, so the file needs a working shebang and at
least one executable permission bit. Directories, non-executable files,
unknown filename prefixes, and symlinks that leave `scripts/` are preparation
errors. A symlink may target an executable file elsewhere below `scripts/`.
At execution time, xoldot reopens the prepared target beneath `scripts/`,
verifies its identity and content, and executes that verified open file.

Scripts inherit standard input, output, and error. xoldot replaces these
environment values for each invocation:

```text
XOLDOT=1
XOLDOT_CONFIG_DIR=/absolute/configuration/path
XOLDOT_TARGET_HOME=/absolute/target/home
XOLDOT_APPLY_COMPONENTS=tools,managed-home,aliases
XOLDOT_PROFILE=work
```

`XOLDOT_APPLY_COMPONENTS` lists the selected Apply parts in their canonical
order, separated by commas. `XOLDOT_PROFILE` is present only when Apply selects
a Profile, and its value is the Profile's normalized lowercase name. Scripts
belong to the complete Configuration and are not filtered by a Profile.

xoldot records successful `run_once_` and `run_onchange_` scripts in
`~/.local/state/xoldot/scripts.json`. It writes one successful result at a
time. For each stateful script, xoldot holds `scripts.lock` while it reloads
eligibility, runs the script, and records success. Concurrent Apply processes
serialize these attempts. A waiting process rechecks eligibility and skips a
script after the prior process records success. A failed script does not
advance its state. A failed before script stops Apply before any selected part
changes the Machine. A failed after script returns an error but does not undo
completed Apply work.

Lifecycle scripts are trusted executable Configuration content. Keep `run_`
scripts idempotent. After scripts should also tolerate a retry when an earlier
attempt completed its Machine work but failed before recording other state.
Lifecycle scripts do not have dependencies, parallel execution, retries,
timeouts, templates, or automatic rollback.

## Profiles

A Profile selects an explicit subset of one Configuration. Put each Profile in
`profiles/<name>.toml`, then pass its name to Apply, Status, or Diff:

```sh
xoldot apply --profile work
xoldot apply --profile work --only tools --only managed-home
xoldot apply --profile work --dry
xoldot status --profile work
xoldot diff --profile work
```

Profile names start and end with a letter or number. Between them, they may
also use underscores and hyphens. Names are case-insensitive and normalize to
lowercase, so `Work.toml` and `work.toml` conflict. A command selects one leaf
Profile. With no `--profile`, these commands use the complete Configuration as
before.

A Profile can select exact catalog names and clean paths relative to
`files/home`:

```toml
extends = ["base", "development"]
tools = ["git", "ripgrep"]
aliases = ["ll", "gs"]
skills = ["unslop"]
managed_home = [".gitconfig", ".config/git"]
```

`extends` may name more than one parent and may span multiple levels. The
result is the union of the leaf and every reachable parent. Parent order does
not affect the result. Profiles have no exclusions, overrides, variables, or
templates.

Tool, Alias, and Skill entries must match names already declared in their
catalogs. A managed-home entry must already exist, must stay within
`files/home`, and cannot contain redundant path components. Selecting a file
selects that file. Selecting a directory selects every file below it.

Selecting a Skill also selects its canonical files under `.agents/skills`, its
Claude compatibility files under `.claude/skills`, and its Companion agents
under `.agents/agents` and `.claude/agents`. The four namespace roots are
reserved. A Profile cannot name a reserved root, a parent of one, or any path
below one through `managed_home`; select the Skill by catalog name instead.

xoldot validates every Profile, inheritance edge, catalog reference, and
managed-home member before a selected Profile can change the Machine. Missing
parents, cycles, normalized-name collisions, unknown members, and unsafe paths
are errors. Profile validation reads the complete Tool, Alias, and Skill
catalogs even when `--only` narrows Apply. Selection then filters the
Configuration before the existing Apply, Status, and Diff planners run.

Apply refuses managed home conflicts by default. `--backup` opts into replacing
conflicting regular files and symlinks. It does not change conflict handling for
Tools or Aliases, and it requires the `managed-home` Apply part. See
[Conflict backup and restore](#conflict-backup-and-restore) before using it.

## Inspect before applying

Inspect the current Machine without changing it:

```sh
xoldot status
```

Status lists each managed home link as current, missing, stale, or conflicting.
It marks regular-file and symlink conflicts that `apply --backup` can save.
It reports the Alias output as current, missing, safely replaceable, or
conflicting. It also verifies installed Skill digests and ownership using local
files.

Status also lists restorable backup runs. An incomplete run has no complete
manifest, which can happen if a process stops outside the normal rollback path.
An invalid run has a malformed manifest, missing or unexpected content, or
stored content that no longer matches its recorded type, mode, and digest.

Status counts declared Tools as unchecked. It does not run their `check`
commands because those commands are user-authored shell code. It also does not
run install commands, `npx`, Git operations, or lifecycle scripts.

When lifecycle scripts are eligible, Status lists them as work that would run.

Show only the pending managed home and Alias work:

```sh
xoldot diff
```

Diff prints the link additions and removals that a dry Apply would plan. For an
owned Alias file that needs replacement, it prints a unified text diff. Missing
Alias output is reported as a planned creation. Content that xoldot cannot
safely replace is reported as a conflict. Diff also reports eligible lifecycle
scripts in before and after phase order.

Both commands are read-only. They do not create the Target home, state
directories, or output files. Drift and conflicts are successful inspection
results. Invalid configuration, unreadable paths, and invalid ownership state
still return an error.

## Troubleshoot

Run Doctor when setup, Apply, Skill management, or Sync cannot proceed:

```sh
xoldot doctor
```

Doctor checks all of these in one run:

- `xoldot.toml`, the Tool, Alias, and Skill catalogs, and every Profile parse
  and validate. Profile checks include inheritance, catalog references, and
  managed-home members.
- Configuration paths, managed-link state, and managed home targets stay within
  their permitted roots and do not create recursion. Skill and Companion-agent
  paths under `.agents` and `.claude` may use explicit directory redirects for
  agent compatibility.
- The managed-link ledger is readable and valid.
- The detected shell is Bash, Zsh, or Fish and is enabled by `aliases.shells`.
- `git` is on `PATH` when Sync or a saved Git-backed Skill source needs it.
- `npx` and Node.js 22.20 or newer are on `PATH` when Skills are declared.
- A Sync-enabled Configuration is a local Git repository with its configured
  remote and branch. These checks use local Git state only.
- Managed home and Alias conflicts are reported through the same ownership
  inspection used by Status.

Doctor prints errors first, then warnings, then information. Each error or
warning includes a remedy. Errors return a failing exit status. Managed home
and Alias conflicts are warnings because they are reportable Machine drift;
warnings and information do not make Doctor fail.
Remedy lines remain indented without a prefix.

Doctor is read-only. It may run `node --version` and local read-only Git
commands. It never runs user-authored Tool checks, invokes `npx`, fetches a Git
remote, authenticates, installs software, or changes or repairs files. It does
not test network access, credentials, general operating-system health, or
whether a generated Alias file is sourced by shell startup files.

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
xoldot's `links.json`, `links.lock`, `scripts.json`, and `scripts.lock` state
files, plus its `backups/` tree, cannot be declared as managed home content.
The lock paths must remain ordinary files. xoldot keeps them private with
`0600` permissions and refuses symlinks or special files in their place.

### Conflict backup and restore

Conflict backup deliberately moves user content. Preview the operation first:

```sh
xoldot apply --backup --only managed-home --dry
xoldot apply --backup --only managed-home
```

Without `--backup`, Apply keeps its normal conflict refusal. With the flag,
xoldot moves each conflicting regular file or symlink into one run below
`~/.local/state/xoldot/backups/<backup-id>/`, writes a manifest, and creates the
planned managed link. The command prints the backup ID. Backup data stays on
the Machine and is not part of the Configuration repository or Sync.

The managed-home transaction rolls back its backups and links if that part of
Apply fails. Other Apply parts remain separate transactions. A later Alias
failure does not undo a completed managed-home backup, and a Tool installation
is never backed up or rolled back by this flag.

Restore the entire run by ID:

```sh
xoldot restore <backup-id> --dry
xoldot restore <backup-id>
```

Restore checks every entry before changing anything. Every target must still be
the xoldot-owned link recorded by the backup run, and every stored file or
symlink must still match the manifest. Restore refuses the whole run if a
target, backup, manifest, or ownership record changed. A successful restore
recreates all original files and symlinks with their recorded permission bits,
drops their managed-link ownership records, and removes the consumed backup
run.

The first release does not back up directories or special files. It does not
delete conflicts, retain generations, prune backups, restore part of a run, or
roll back Tools and Aliases. Backup and restore refuse paths that escape the
Target home, including paths redirected through an outside symlink. Do not edit
the state directory. If it is deleted or damaged, xoldot cannot restore that
backup.

## Sync

```sh
xoldot sync
```

Sync commits local changes as `xoldot sync`, pulls the configured branch with
rebase, and pushes it. Git provides the author identity and remote
authentication. Sync always synchronizes the complete Configuration repository
and does not select a Profile.

Inspect the Machine or preview adoption, Apply, Restore, and Sync without
changing anything:

```sh
xoldot status
xoldot diff
xoldot adopt ~/.config/git/config --dry
xoldot doctor
xoldot apply --dry
xoldot apply --backup --only managed-home --dry
xoldot restore <backup-id> --dry
xoldot sync --dry
```

Dry adoption prints the exact move and link. Dry Apply does not run selected
Tool checks because they are user-authored shell commands. Unlike `xoldot diff`,
dry Apply also describes what each selected Tool check and installation would
do. A dry backup reports eligible conflicts and refuses directories, special
files, and paths outside the Target home without writing state. Dry restore
checks the same all-or-nothing preconditions as restore and changes nothing.
Dry Apply also reports eligible lifecycle scripts in execution order without
running them or writing script state.

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
- Color controls are documented under [Output](#output).

Force deletion and shells other than Bash, Zsh, and Fish are not supported.
Profiles cannot select themselves from a hostname, operating system,
distribution, architecture, or environment value.

Contributor build, test, and release instructions are in
[DEVELOPMENT.md](DEVELOPMENT.md).
