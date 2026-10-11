# SPDX-License-Identifier: GPL-3.0-or-later

"""Load sonda QoE rows from the ptnop archive and the historical qoe Parquet files.

Both sources produce the same columns, matching the `metricsRow` schema that
`sonda etl-ptnop-qoe` used to write: one row per Done event of the
connect, DNS exchange, TLS handshake, and HTTP round trip operations.
"""

import json
from compression import zstd
from pathlib import Path

import numpy as np
import pandas as pd

DEFAULT_ARCHIVE_DIR = Path("/var/lib/sonda/archive/ptnop")
DEFAULT_QOE_DIR = Path("/var/lib/sonda/metrics/qoe")

DONE_EVENTS = frozenset({
    "connectDone",
    "dnsExchangeDone",
    "httpRoundTripDone",
    "tlsHandshakeDone",
})

COLUMNS = [
    "span_id",
    "msg",
    "t0",
    "t",
    "duration_us",
    "local_addr",
    "remote_addr",
    "protocol",
    "err_class",
    "server_protocol",
    "http_response_status_code",
    "reflexive_addr_v4",
    "reflexive_addr_v6",
]

_STRING_COLUMNS = [
    "span_id",
    "msg",
    "local_addr",
    "remote_addr",
    "protocol",
    "err_class",
    "server_protocol",
    "reflexive_addr_v4",
    "reflexive_addr_v6",
]

# Cheap substring test run before json.loads, which dominates the load time.
# We match the quoted event name alone so the test does not depend on how
# the JSON is formatted: false positives are fine, json.loads sorts them out.
_DONE_MARKERS = tuple(f'"{m}"' for m in sorted(DONE_EVENTS))


def empty_frame() -> pd.DataFrame:
    """Return an empty frame with the expected columns."""
    return pd.DataFrame({name: pd.Series(dtype=object) for name in COLUMNS})


def load_qoe_dir(path: Path) -> pd.DataFrame:
    """Load the daily qoe Parquet files below path, recursively.

    Returns an empty frame if path does not exist or contains no files.
    """
    files = sorted(Path(path).rglob("*.parquet"))
    if len(files) <= 0:
        return empty_frame()
    return pd.concat([pd.read_parquet(f) for f in files], ignore_index=True)


def archive_files(path: Path) -> list[Path]:
    """Return the archive daily files below path, sorted by name.

    When a day exists both compressed and uncompressed, which happens
    briefly while the archiver compresses it, we only return the
    compressed file, which is complete once it has its final name.
    """
    path = Path(path)
    if not path.is_dir():
        return []
    compressed = {p.name.removesuffix(".zst"): p for p in path.glob("*.jsonl.zst")}
    plain = {p.name: p for p in path.glob("*.jsonl") if p.name not in compressed}
    return [p for _, p in sorted((compressed | plain).items())]


def event_to_row(ev: dict) -> dict | None:
    """Map a ptnop event to a qoe row, or return None to skip it.

    Mirrors `extractParseSpan` and `extractEventToMetrics` in
    `cmd/sonda-etl-ptnop-qoe/extract.go`: we keep the Done events,
    skip ESKIP, and map empty optional fields to None.
    """
    if ev.get("msg") not in DONE_EVENTS:
        return None
    err_class = ev.get("errClass") or None
    if err_class == "ESKIP":
        return None
    return {
        "span_id": ev.get("spanId", ""),
        "msg": ev["msg"],
        "t0": ev.get("t0"),
        "t": ev.get("t"),
        "local_addr": ev.get("localAddr", ""),
        "remote_addr": ev.get("remoteAddr", ""),
        "protocol": ev.get("protocol", ""),
        "err_class": err_class,
        "server_protocol": ev.get("dnsServerProtocol") or None,
        "http_response_status_code": ev.get("httpResponseStatusCode") or None,
        "reflexive_addr_v4": ev.get("reflexiveAddrV4") or None,
        "reflexive_addr_v6": ev.get("reflexiveAddrV6") or None,
    }


def read_archive_file(path: Path) -> list[dict]:
    """Return the qoe rows in an archive daily file.

    Lines that do not parse are skipped: today's file may end with
    a partially written line while the archiver appends to it.
    """
    path = Path(path)
    opener = zstd.open if path.suffix == ".zst" else open
    rows = []
    with opener(path, "rt", encoding="utf-8") as fp:
        for line in fp:
            if not any(m in line for m in _DONE_MARKERS):
                continue
            try:
                ev = json.loads(line)
            except json.JSONDecodeError:
                continue
            if not isinstance(ev, dict):
                continue
            row = event_to_row(ev)
            if row is not None:
                rows.append(row)
    return rows


def _go_micros(delta_ns: pd.Series) -> pd.Series:
    """Convert nanoseconds to microseconds truncating toward zero, like Go's Duration.Microseconds."""
    ns = delta_ns.to_numpy(dtype=np.int64)
    return pd.Series(np.sign(ns) * (np.abs(ns) // 1000), index=delta_ns.index)


def rows_to_frame(rows: list[dict]) -> pd.DataFrame:
    """Build a frame with the qoe columns and dtypes from rows."""
    if len(rows) <= 0:
        return empty_frame()
    df = pd.DataFrame.from_records(rows)

    # 1. parse the timestamps at nanosecond precision, which carry
    # their own UTC offset, and compute the duration as Go does.
    t0 = pd.to_datetime(df["t0"], format="ISO8601", utc=True)
    t = pd.to_datetime(df["t"], format="ISO8601", utc=True)
    df["duration_us"] = _go_micros((t - t0).dt.as_unit("ns").astype("int64"))

    # 2. truncate the timestamps to microseconds, like Go's UnixMicro
    # for times after the epoch, to match the Parquet files.
    df["t0"] = t0.dt.floor("us").dt.as_unit("us")
    df["t"] = t.dt.floor("us").dt.as_unit("us")

    # 3. match the Parquet dtypes, including for all-null columns.
    for name in _STRING_COLUMNS:
        df[name] = df[name].astype("str")
    df["http_response_status_code"] = df["http_response_status_code"].astype("float64")
    return df[COLUMNS]


def load_archive_dir(path: Path) -> pd.DataFrame:
    """Load the archive daily files below path.

    Returns an empty frame if path does not exist or contains no files.
    """
    rows = []
    for f in archive_files(path):
        rows.extend(read_archive_file(f))
    return rows_to_frame(rows)


def merge(archive: pd.DataFrame, qoe: pd.DataFrame) -> pd.DataFrame:
    """Concatenate archive and qoe rows, dropping qoe rows for spans the archive has.

    The archive is a superset of the qoe data for each span it contains,
    so we resolve overlaps per span and never mix rows of the two sources
    within the same span.
    """
    if archive.empty:
        return qoe.reset_index(drop=True)
    if qoe.empty:
        return archive.reset_index(drop=True)
    qoe = qoe[~qoe["span_id"].isin(set(archive["span_id"]))]
    return pd.concat([archive, qoe], ignore_index=True)


def load(archive_dir: Path | None, qoe_dir: Path | None) -> pd.DataFrame:
    """Load and merge the given sources. A None source is not loaded."""
    archive = load_archive_dir(archive_dir) if archive_dir is not None else empty_frame()
    qoe = load_qoe_dir(qoe_dir) if qoe_dir is not None else empty_frame()
    return merge(archive, qoe)
