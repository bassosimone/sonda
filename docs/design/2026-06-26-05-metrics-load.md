---
author: sbs
status: active
---

# Design: `sonda metrics load`

## Purpose

Aggregates per-span `metrics.parquet` files from the spool into
daily Parquet files under a persistent directory. The spool is
ephemeral — GC deletes spans after a few hours — so metrics must
be copied out before they disappear. This command bridges the gap
between the short-lived spool and long-term analysis.

## Output layout

Daily files are organized by date:

```
metricsDir/YYYY/MM/DD/YYYY-MM-DD.parquet
```

Each daily file contains all rows from all spans whose UUIDv7
timestamp falls on that UTC day. The schema is identical to the
per-span `metrics.parquet` produced by `sonda spool extract` —
the same `structured.Metrics` struct, no transformations.

## Idempotency and concurrency

### Sentinel files

After successfully appending a span's rows to the daily file,
the command creates a `metrics.loaded` sentinel file inside the
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
Since `O_EXCL` ensures only one process appends per span, the
daily file is never concurrently modified.

## Append strategy

The command reads the entire existing daily file into memory,
appends the new rows, and writes everything back. This is a
full rewrite on each span.

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
from `sonda spool extract` are uncompressed (they are small and
ephemeral). The reader handles both transparently.

At current data rates, zstd shrinks the daily file from ~570 KB
to ~100 KB — roughly 5.7x. The improvement comes from
dictionary encoding on repeated string columns (`msg`,
`protocol`, `remote_addr`) that zstd compresses effectively.

## Flags

- `--spool-dir DIR` — root of the spool tree (default: `.`).

- `--metrics-dir DIR` — root of the daily metrics tree (default: `.`).

- `--max-age DURATION` — ignore spans older than this (default: `24h`).

## Integration with `sonda scan`

`sonda scan` invokes `sonda metrics load` as a subprocess after
`sonda spool extract` and before `sonda spool gc`. The ordering
matters: extract must create `metrics.parquet` before load can
read it, and load must copy metrics out before GC deletes the
span.

The systemd service passes `--metrics-dir /var/lib/sonda/metrics`
and `--spool-dir /var/spool/sonda`. The metrics directory is
created by the Debian `postinst` with `_sonda:adm` ownership
and `2750` permissions, matching the spool directory. The systemd
unit's `ReadWritePaths` includes both directories.

## What this is not

- Not an analysis tool. It writes Parquet; pandas reads it.

- Not a retention policy. Daily files accumulate indefinitely.
  A future command or cron job could prune old daily files.

- Not a deduplication layer. The `O_EXCL` sentinel prevents
  duplicates at the source. There is no row-level dedup.
