---
author: sbs
status: active
---

# Design: migration to ptnop and plugins

## Purpose

Explain why sonda moved its measurements from `nop` to `ptnop`,
and why this move also turned the subcommands into plugins named
after the engine and the data they handle.

## Why ptnop

- ptnop emits events for every pipeline stage, including the stages
that run after an earlier failure (`errClass` is `ESKIP`).

- So each event stream serializes to a table where every row has
every column filled, without additional context from the caller.

- See `go doc github.com/bassosimone/ptnop`.

## Why not adapt `internal/netstack`

- The first plan was to keep `internal/netstack` and use ptnop as its
backend.

- netstack imitated the standard library: it exposed Go types with
familiar signatures and moved tags around using the context. With
ptnop, this meant converting results back and forth.

- A simpler design was to always run the measurement as a subcommand
and give the caller accessors over the resulting span (now
`internal/ptnopspool`). netstack is kept as a historical document
(`2026-06-25-02-netstack.md`).

## Why name things after the engine

- We expect more than one measurement engine (e.g., MSAK) and more
than one way of processing each engine's output.

- So, the code that only makes sense for ptnop says so in its name:
`internal/ptnopdata` (structured log schema), `internal/ptnoppaths`
(spool files), `internal/ptnopspool` (span accessors).

- Likewise, plugins carry the engine and the data they produce:
`measure-<engine>` (e.g., `measure-ptnop`) and
`etl-<source>-<dest>` (e.g., `etl-ptnop-qoe`, which used to be
`sonda metrics`).

- Each engine's output is also its own data type, so the spool and
the metrics directories separate data by type. See
`2026-10-04-00-data-types.md`.

- Plugins also separate concerns at the process level. See
`2026-09-28-00-plugins.md`.
