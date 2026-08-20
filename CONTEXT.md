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
Reconciliation of a Configuration with a Machine. It ensures Tools are present,
exposes managed home content, and renders Aliases for the selected shell.
_Avoid_: Install, Sync

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
