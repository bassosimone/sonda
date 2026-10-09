---
author: sbs
status: active
---

# Design: `sonda-etl-ptnop-obs`

## Purpose

Keep a long-lived copy of the most useful ptnop events, so that we
can analyze them after `sonda spool gc` has removed the spans.

It is a second try after `etl-ptnop-qoe`, which still runs from
its timer. It copies events rather than deriving metrics: the
analysis happens later, from the copy.

## How it works

- Started by `sonda-etl-ptnop-obs.path` (see
`2026-10-09-00-triggers.md`). It reads every work unit in
`/run/sonda/etl-ptnop-obs` and removes it.

- For each `ptnop` span younger than `--max-span-age` (24h), it
checks that `spanDir` matches the path derived from `spanId`, then
copies from `stdout.txt` the events whose `msg` is `connectDone`,
`dnsExchangeDone`, `dnsResponse`, `httpBodyStreamDone`,
`httpRoundTripDone`, or `tlsHandshakeDone`.

- Lines are copied verbatim. Empty lines are skipped; lines that do
not parse as events are logged and skipped.

## Output

- `/var/lib/sonda/metrics/obs/YYYYMMDDT000000Z.jsonl`, one file per
UTC day of the span timestamp, opened with `O_APPEND`. Event `time`
fields keep the local offset.

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

- Readers should therefore skip empty lines, warn about and skip
unparsable or too long lines, and drop duplicate lines.

## Open questions

- Size: a first scan produced about 202 KB, so about 58 MB/day at
one scan every five minutes. After a few days of data, decide
whether to compress closed days or convert them to Parquet.

- Retention: nothing removes old files yet (see the TODOs in
`main.go`).

- What to do with `etl-ptnop-qoe` once the above is clear.
