# SPDX-License-Identifier: GPL-3.0-or-later

"""Tests for the archive and qoe loader."""

import json
from compression import zstd

import pandas as pd

from research import loader

SPAN_A = "01a12a7d-950d-7340-9fa2-fc2710843bbb"
SPAN_B = "01a12a7d-9543-77a0-a7fb-95aa6e446667"


def _event(msg, span_id=SPAN_A, **extra):
    ev = {
        "level": "INFO",
        "msg": msg,
        "spanId": span_id,
        "err": None,
        "errClass": "",
        "localAddr": "10.0.0.1:33869",
        "protocol": "udp",
        "remoteAddr": "8.8.8.8:53",
        "t0": "2026-10-11T12:23:55.661714958+02:00",
        "t": "2026-10-11T12:23:55.661778811+02:00",
    }
    ev.update(extra)
    return ev


def _lines(*events):
    return "".join(json.dumps(ev) + "\n" for ev in events)


def test_event_to_row_skips_non_done_and_eskip():
    assert loader.event_to_row(_event("connectStart")) is None
    assert loader.event_to_row(_event("closeDone")) is None
    assert loader.event_to_row(_event("connectDone", errClass="ESKIP")) is None


def test_event_to_row_maps_optional_fields():
    row = loader.event_to_row(_event(
        "httpRoundTripDone",
        errClass="ETIMEDOUT",
        httpResponseStatusCode=200,
        reflexiveAddrV4="192.0.2.1",
    ))
    assert row["err_class"] == "ETIMEDOUT"
    assert row["http_response_status_code"] == 200
    assert row["reflexive_addr_v4"] == "192.0.2.1"
    assert row["reflexive_addr_v6"] is None
    assert row["server_protocol"] is None


def test_rows_to_frame_times_and_duration():
    df = loader.rows_to_frame([loader.event_to_row(_event("connectDone"))])
    assert list(df.columns) == loader.COLUMNS
    # 12:23:55.661714958+02:00 truncated to microseconds, in UTC.
    assert df["t0"][0] == pd.Timestamp("2026-10-11T10:23:55.661714Z")
    # 661778811 - 661714958 = 63853 ns, i.e., 63 us.
    assert df["duration_us"][0] == 63
    assert str(df["t0"].dtype) == "datetime64[us, UTC]"


def test_rows_to_frame_negative_duration_truncates_toward_zero():
    ev = _event("connectDone", t0="2026-10-11T10:00:00.000001500Z", t="2026-10-11T10:00:00Z")
    df = loader.rows_to_frame([loader.event_to_row(ev)])
    assert df["duration_us"][0] == -1


def test_read_archive_file_skips_garbage_and_partial_lines(tmp_path):
    path = tmp_path / "20261011T000000Z.jsonl"
    text = _lines(_event("connectStart"), _event("connectDone"))
    text += "not json\n"
    text += json.dumps(_event("dnsExchangeDone"))[:40]  # partial last line
    path.write_text(text)
    rows = loader.read_archive_file(path)
    assert [r["msg"] for r in rows] == ["connectDone"]


def test_archive_files_prefers_compressed(tmp_path):
    (tmp_path / "20261009T000000Z.jsonl.zst").write_bytes(zstd.compress(b""))
    (tmp_path / "20261009T000000Z.jsonl").write_text("")
    (tmp_path / "20261010T000000Z.jsonl").write_text("")
    (tmp_path / "20261010T000000Z.jsonl.zst.tmp").write_text("")
    names = [p.name for p in loader.archive_files(tmp_path)]
    assert names == ["20261009T000000Z.jsonl.zst", "20261010T000000Z.jsonl"]


def test_archive_files_missing_dir(tmp_path):
    assert loader.archive_files(tmp_path / "nonexistent") == []
    assert loader.load_archive_dir(tmp_path / "nonexistent").empty


def test_load_archive_dir_reads_compressed(tmp_path):
    data = _lines(_event("connectDone"), _event("dnsExchangeDone", span_id=SPAN_B))
    (tmp_path / "20261011T000000Z.jsonl.zst").write_bytes(zstd.compress(data.encode()))
    df = loader.load_archive_dir(tmp_path)
    assert sorted(df["span_id"]) == [SPAN_A, SPAN_B]


def test_merge_prefers_archive_per_span():
    archive = loader.rows_to_frame([loader.event_to_row(_event("connectDone"))])
    qoe = loader.rows_to_frame([
        loader.event_to_row(_event("dnsExchangeDone")),
        loader.event_to_row(_event("connectDone", span_id=SPAN_B)),
    ])
    df = loader.merge(archive, qoe)
    assert sorted(zip(df["span_id"], df["msg"])) == [
        (SPAN_A, "connectDone"),
        (SPAN_B, "connectDone"),
    ]


def test_merge_with_empty_sources():
    archive = loader.rows_to_frame([loader.event_to_row(_event("connectDone"))])
    assert len(loader.merge(archive, loader.empty_frame())) == 1
    assert len(loader.merge(loader.empty_frame(), archive)) == 1
    assert loader.merge(loader.empty_frame(), loader.empty_frame()).empty
