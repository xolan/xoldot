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

Release tags must use the stable `vMAJOR.MINOR.PATCH` form. After the target
commit has passed CI on `main`, create and push an annotated tag:

```sh
git tag -a v0.1.0 -m "xoldot v0.1.0"
git push origin v0.1.0
```

The release workflow runs all repository checks. It then cross-compiles Linux
and macOS archives for `amd64` and `arm64` with `CGO_ENABLED=0`. The publish job
creates a GitHub Release containing those archives and `SHA256SUMS`. The tag is
also the version reported by `xoldot version`.

The workflow rejects other tag forms. A failed verification or build does not
publish a release.
