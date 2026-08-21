# xoldot

xoldot manages a portable personal command-line environment. A Configuration
records the user's intent, and Apply brings a Machine toward that intent without
taking over unrelated user content.

## Environment

**Configuration**:
The complete declaration xoldot manages for one personal command-line
environment, including tools, aliases, skills, and managed home content.
_Avoid_: State, settings

**Configuration directory**:
The local working copy of a Configuration used by xoldot commands. It may be
independent or Git-backed.
_Avoid_: Config home, xoldot home

**Configuration repository**:
A Configuration directory with Git synchronization enabled.
_Avoid_: Configuration directory when Git is disabled

**Profile**:
A named subset of a Configuration for one Machine. A Profile may inherit from
other Profiles, and it selects declarations without redefining them.
_Avoid_: Environment, Machine configuration

**Machine**:
The operating-system environment affected by Apply, including its available
tools and Target home.
_Avoid_: Configuration, host

**Target home**:
The home-directory destination for managed home content and rendered aliases on
a Machine.
_Avoid_: Managed home, real home

**Managed home content**:
The path-preserving collection of files and agent content that a Configuration
intends to expose under the Target home.
_Avoid_: Dotfiles, Target home

## Declared capabilities

**Tool**:
A named Machine capability whose presence xoldot can test and, when absent,
install using platform-specific instructions. Removing a Tool from the
Configuration does not uninstall it.
_Avoid_: Package, dependency

**Alias**:
A portable name-command pair that xoldot can express in a supported shell.
_Avoid_: Shell function, script

**Skill**:
A named package of agent instructions acquired from a Skill source and managed
as one unit.
_Avoid_: Agent, plugin

**Skill source**:
The saved origin from which xoldot can install or update a Skill.
_Avoid_: Repository, because a local directory can also be a Skill source

**Companion agent**:
An agent definition distributed with, referenced by, and managed as part of a
Skill. A Companion agent can belong to only one Skill in a Configuration.
_Avoid_: Skill, Codex agent configuration

## Operations and ownership

**Setup**:
Initialization of a Configuration directory, with optional Git synchronization.
Setup does not apply the Configuration to a Machine.
_Avoid_: Apply, bootstrap

**Apply**:
Reconciliation of a Configuration with a Machine. It reconciles every Apply
part by default, or only the parts selected by the user.
_Avoid_: Install, Sync

**Apply part**:
One of the independently selectable parts of Apply: Tools, managed home
content, or Aliases.
_Avoid_: Component, Step

**Status**:
A read-only comparison of a Configuration with a Machine. Status reports
managed home, Alias, and locally verifiable Skill state without running Tool
checks.
_Avoid_: Apply, because Status does not change the Machine

**Diff**:
A read-only view of the managed home and Alias changes that Apply would make.
_Avoid_: Status, because Diff shows planned changes rather than all inspected
state

**Adopt**:
Transactional transfer of one existing ordinary file from the Target home to
the matching managed home content path. Adopt replaces the original with an
xoldot-owned link, but does not Apply or Sync. It serializes ownership-ledger
updates and restores only transaction-owned paths that have not been replaced.
_Avoid_: Apply, import

**Doctor**:
A read-only diagnosis of whether a Configuration and Machine meet xoldot's
operating requirements, with a remedy for each problem.
_Avoid_: Status, because Doctor checks operating requirements rather than drift

**Conflict backup**:
An opt-in Apply result that preserves displaced managed home conflicts as one
restorable set. A Conflict backup does not confer ownership over the preserved
content.
_Avoid_: Snapshot, force

**Restore**:
All-or-nothing replacement of the verified managed links from one Conflict
backup with the user content that backup preserved.
_Avoid_: Apply, partial restore

**Sync**:
Git synchronization of a Configuration repository. It commits Configuration
changes, rebases from the configured remote branch when present, and pushes,
but does not Apply the Configuration.
_Avoid_: Apply

**Xoldot-owned content**:
Content that xoldot created or installed and can still verify as unchanged.
xoldot may replace or remove only content it owns; other existing content is a
conflict.
_Avoid_: Managed home content, user-owned content
