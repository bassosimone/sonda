---
author: sbs
status: active
---

# Design: migration to ptnop and plugins

## Purpose

Record what changed between v0.5.0 and the next release, and why.
The other design documents describe the current state.

## What changed

- `sonda measure` (built on `nop`) became the `measure-ptnop` plugin
(built on `ptnop`).

- `internal/netstack` became `internal/ptnopspool`. The structured
log parser and path helpers became `internal/ptnopdata` and
`internal/ptnoppaths`.

- `sonda spool`, `sonda scan`, and `sonda metrics` became plugins.
`sonda metrics` is now `etl-ptnop-qoe`. The `sonda` binary only
dispatches. See `2026-09-28-00-plugins.md`.

## Why ptnop

- ptnop emits events for every pipeline stage, including the stages
that run after an earlier failure (`errClass` is `ESKIP`).

- So each event stream serializes to a table where every row has
every column filled, without additional context from the caller.

- See `go doc github.com/bassosimone/ptnop`.

## Naming convention

- Measurement plugins: `measure-<engine>` (e.g., `measure-ptnop`).
Results go to `$spoolDir/<engine>`.

- ETL plugins: `etl-<source>-<dest>` (e.g., `etl-ptnop-qoe`). They
read `$spoolDir/<source>` and write `$metricsDir/<dest>`.

- Per-span files: `<dest>.parquet` and `<dest>.loaded`, so several
ETL plugins can process the same span.

- See `2026-06-25-01-spool-run.md` and `2026-06-26-03-spool-extract.md`.

## Breaking changes since v0.5.0

- Spool and metrics move to `$spoolDir/ptnop` and `$metricsDir/qoe`.
There is no automatic migration: after upgrading, `gc` does not
remove spans left directly under `$spoolDir`.

- `metrics.parquet` and `metrics.loaded` become `qoe.parquet` and
`qoe.loaded`.

- The `spanID` log key becomes `spanId`, like the other keys.

- ptnop renamed `httpMethod`, `httpUrl`, and `serverProtocol` to
`httpRequestMethod`, `httpRequestUrl`, and `dnsServerProtocol`.
The Parquet column is still `server_protocol`.

- `spool run` only runs sonda subcommands and always generates the
span ID: `--span-id` is gone.

- `scan` drops `-f` (use `--fail`) and requires `--config-file`.
