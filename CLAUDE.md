# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

sonda is an experimental Go CLI probe that periodically measures DNS, STUN, and other
network properties, storing structured results in a local spool for later analysis.

## Build and Test Commands

```bash
go build .
go test ./...
```

There is no Makefile or CI configuration. Packaging is handled by `scripts/makedeb.bash`.

## Architecture

The root `main.go` wires a `vclip` dispatcher; each subcommand lives in its own package
under `internal/cli/`. Subcommands receive `context.Context` and `args []string`.

Side effects (filesystem, exec, stdio, `os.Exit`) are abstracted through `internal/testable/`.

## Conventions

- `len(x) <= 0` rather than `== 0` for emptiness checks.
