# Example configuration

This directory is a complete Configuration in the layout created by `xoldot
setup`. It uses harmless shell commands, fictional names, and paths contained
within the example.

`xoldot.toml` spells out every default setting. Git sync is disabled because
`git.enabled` is `false`. Running `xoldot setup` with a remote URL changes that
setting to `true` and configures the remote; see [Sync](../../README.md#sync)
before enabling it.

The Profiles share `base`:

- `work` adds the `worktree` Alias and the regular file
  `.config/workbench.toml`.
- `personal` adds the `weekend` Alias and the managed directory
  `.config/leisure`.
- Both select the `focus` Skill through `base`. That Skill selection implicitly
  selects its `.agents` and `.claude` managed-home paths. The Profiles do not
  list those reserved paths in `managed_home`.

The `focus` Skill source and digest are placeholders that only demonstrate the
catalog schema. Loading this Configuration and inspecting its Profiles does not
contact that source. Replace the catalog entry by running `skill add` before
trying to install or update the Skill; those operations use `npx` and may need
Git or network access depending on the real source.

To inspect or dry-run the example without using your Configuration or home,
copy it to a temporary directory and set `XOLDOT_TARGET_HOME`:

```sh
scratch="$(mktemp -d)"
cp -R examples/configuration "$scratch/config"
mkdir "$scratch/home"

xoldot --config-dir "$scratch/config" profile list
xoldot --config-dir "$scratch/config" profile show work
XOLDOT_TARGET_HOME="$scratch/home" \
  xoldot --config-dir "$scratch/config" apply --profile work --dry
```

The dry run explains the selected Tool, Alias, managed-home content, and
lifecycle scripts without changing the temporary home. See
[Profiles](../../README.md#profiles), [Apply](../../README.md#apply), and
[Lifecycle scripts](../../README.md#lifecycle-scripts) for their full
contracts.
