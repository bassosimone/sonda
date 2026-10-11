---
author: sbs
status: active
---

# Design: `sonda-etl-ptnop-archive`

## Purpose

Keep a long-lived copy of the ptnop events, so that we
can analyze them after `sonda spool gc` has removed the spans.

## How it works

- Started by `sonda-etl-ptnop-archive.path` (see
`2026-10-09-00-triggers.md`). It reads every work unit in
`/run/sonda/etl-ptnop-archive` and removes it.

- For each `ptnop` span younger than `--max-span-age` (24h), it
checks that `spanDir` matches the path derived from `spanId`, then
copies every event from `stdout.txt`.

- Lines are copied verbatim. Empty lines are skipped; lines that do
not parse as events are logged and skipped.

## Output

- `/var/lib/sonda/archive/ptnop/YYYYMMDDT000000Z.jsonl`, one file per
UTC day of the span timestamp, opened with `O_APPEND`. Event `time`
fields keep the local offset.

- At the end of each run, it compresses the days no span can be
appended to anymore into `YYYYMMDDT000000Z.jsonl.zst`. A day is closed
when it ended at least `--max-span-age` ago, since we skip older spans.

- We use zstd: on one day of data it gives about 19x (30 MB to
1.5 MB), against about 6x for gzip on one hour of spans. DuckDB's
`read_json` can read `.zst` files for later analysis.

- It then removes the daily files older than `--max-days`
(`archive.ptnop.max-days`, default 90, zero means never). Days,
rather than a duration, since we remove whole daily files.

- A line is at most 512 KiB (`extractMaxLineSize`), well below the
16 MiB default `maximum_object_size` of DuckDB's `read_json`.

## Failure modes

- ENOSPC, a crash during `write`, or a power loss may leave a
truncated line, and the next line is then appended to it. We do not
repair this: the file stays strict JSONL and we accept losing the
affected lines.

- A work unit is removed even when processing fails, so the
affected spans are not retried.

- If the process is killed after writing but before removing the
work unit, the next run copies the same spans again.

- Compression writes a temporary file, fsyncs it, renames it, and
only then removes the uncompressed file, since a power loss could
otherwise lose a whole day. A failed fsync is final: we keep the
uncompressed file and try again from it on the next run, rather than
retrying the fsync, since Linux may then report success without
having written the data (fsyncgate).

- Readers should therefore skip empty lines, warn about and skip
unparsable or too long lines, and drop duplicate lines.

## Open questions

- What to do with `etl-ptnop-qoe`.
