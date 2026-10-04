---
author: sbs
status: active
---

# Design: `sonda etl-ptnop-qoe load`

## Purpose

Aggregates per-span `qoe.parquet` files from the spool into
daily Parquet files under a persistent directory. The spool is
ephemeral — GC deletes spans after a few hours — so metrics must
be copied out before they disappear. This command bridges the gap
between the short-lived spool and long-term analysis.

It shares the `etl-ptnop-qoe` plugin with `extract` because
both depend on the same metric schema.

## Output layout

Daily files are organized by date:

```
$metricsDir/YYYY/MM/DD/YYYY-MM-DD.parquet
```

Each daily file contains all rows from all spans whose UUIDv7
timestamp falls on that UTC day. The schema is identical to the
per-span `qoe.parquet` produced by `sonda etl-ptnop-qoe extract` —
the same `metricsRow` struct, no transformations.

## Idempotency and concurrency

### Sentinel files

After successfully appending a span's rows to the daily file,
the command creates a `qoe.loaded` sentinel file inside the
span directory. On subsequent runs, spans with a sentinel are
skipped. This ensures each span's rows are loaded exactly once.

### Atomic sentinel creation

The sentinel is created with `O_CREATE|O_EXCL` before the
append begins, not after. This eliminates the TOCTOU race where
two concurrent loaders both check for the sentinel, both find it
absent, and both append the same rows — duplicating data.

With `O_EXCL`, only one process can create the sentinel. The
loser gets `EEXIST` and skips the span. If the append fails
after the sentinel is created, the sentinel is removed so the
span can be retried on the next run.

### Atomic daily file writes

The daily Parquet file is written via a temporary file and
`os.Rename`, so readers never see a partially written file.

### Lock file

`O_EXCL` ensures that only one process appends a given span,
but it does not serialize the daily file rewrites. Two loaders
handling different spans of the same day would both read the
daily file, append their own rows, and rename: the last rename
wins and the other loader's rows are lost. They would also share
the same temporary file name.

To prevent this, `load` holds an exclusive lock on
`$metricsDir/lock` for the whole run, using
`github.com/rogpeppe/go-internal/lockedfile`. The lock is blocking:
a manual run started during a timer run waits for that `load`
to finish. The lock lives inside the metrics directory because it
protects that directory and follows `--metrics-dir`. The lock file
is empty and persists after the run, which is harmless because the
lock is released when the process exits.

## Append strategy

The command first walks the spool and groups the candidate spans
by UTC day. Then, for each day, it claims the spans, reads their
rows, reads the entire existing daily file into memory, appends
the new rows, and writes everything back. This is one full rewrite
per day touched by the run, regardless of how many spans it loads.

If a span cannot be read or has no rows, its sentinel is removed
and the other spans of the day are still loaded. If the daily
rewrite fails, the sentinels of all the spans of that day are
removed, so a later run retries them.

An earlier version rewrote the daily file once per span. This
was quadratic in the number of spans for bulk loads, and made
each scan cycle pay one rewrite of a growing daily file per new
span. On a copy of the spool with 1064 spans, all falling on the
same day, the batched version took 0.28s instead of 5.2s and
produced an identical daily file.

This works because:

- Each span contributes ~4 rows.

- A full day at the current scan interval (~288 scans) produces
  ~5000 rows.

- Even at 10x the current measurement surface, a daily file
  stays well under a few megabytes.

If the measurement surface grows enough to make this expensive,
the strategy can change without affecting consumers — the daily
file format is just Parquet rows.

## Compression

Daily files are written with zstd compression. Per-span files
from `sonda etl-ptnop-qoe extract` are uncompressed (they are small and
ephemeral). The reader handles both transparently.

At current data rates, zstd shrinks the daily file from ~570 KB
to ~100 KB — roughly 5.7x. The improvement comes from
dictionary encoding on repeated string columns (`msg`,
`protocol`, `remote_addr`) that zstd compresses effectively.

## Flags

- `--spool-dir DIR` — root of the spool tree (default: `.`).

- `--metrics-dir DIR` — root of the daily metrics tree (default: `.`).

- `--max-age DURATION` — ignore spans older than this (default: `24h`).

## Scheduling

The ETL service (`sonda-etl-ptnop-qoe.service`) runs
`sonda etl-ptnop-qoe load` after `sonda etl-ptnop-qoe extract`, from an
hourly timer. The ordering matters: extract must create
`qoe.parquet` before load can read it, and the oneshot service skips
load when extract fails. Load must also copy metrics out before GC
deletes the span: GC runs from its own timer and removes spans older
than `6h` by default, which leaves several hourly runs of margin.

`--spool-dir` and `--metrics-dir` are the top-level directories
(default: `/var/spool/sonda` and `/var/lib/sonda/metrics`), and
`load` appends its data types: it reads `<spool-dir>/ptnop` and
writes `<metrics-dir>/qoe`.

The metrics directory is created by the Debian `postinst`
with `_sonda:_sonda` ownership and `2750` permissions, matching
the spool directory. The ETL unit's `ReadWritePaths=`
includes both directories.

## What this is not

- Not an analysis tool. It writes Parquet; pandas reads it.

- Not a retention policy. Daily files accumulate indefinitely.
  A future command or cron job could prune old daily files.

- Not a deduplication layer. The `O_EXCL` sentinel prevents
  duplicates at the source. There is no row-level dedup.
