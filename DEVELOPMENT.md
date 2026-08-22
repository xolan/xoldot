# Develop xoldot

[mise](https://mise.jdx.dev/) manages the repository's development tools and
commands.

## Set up the repository

```sh
mise trust
mise install
mise run check
mise run install
```

Use the mise tasks instead of invoking `go`, `gofmt`, or `golangci-lint`
directly. Run `mise tasks` to list them. Set `XOLDOT_TARGET_HOME` to test Apply
against an isolated home directory.

## Build a release archive locally

Set `VERSION`, `GOOS`, and `GOARCH`, then run the archive task:

```sh
VERSION=v0.1.0 GOOS=linux GOARCH=amd64 mise run archive
```

The task disables CGO by default. It writes the binary and archive to `dist/`
with the version, operating system, and architecture in their names.

## Publish a release

After the target commit has passed CI on `main`, create a GitHub Release. Select
or create a tag using the stable `vMAJOR.MINOR.PATCH` form, target the intended
commit, write the release notes, and publish it.

Publishing the Release starts the release workflow. It checks out the selected
tag, runs all repository checks, then cross-compiles Linux and macOS archives
for `amd64` and `arm64` with `CGO_ENABLED=0`. The final job uploads those
archives and `SHA256SUMS` to the same GitHub Release. The tag is also the version
reported by `xoldot version`.

The workflow rejects other tag forms. A failed verification or build leaves the
published Release without generated assets; rerun the failed workflow after
fixing the problem.
