# SPDX-License-Identifier: GPL-3.0-or-later

"""Streamlit app for exploring sonda metrics."""

import datetime
import sys
from pathlib import Path

import numpy as np
import pandas as pd
import plotly.graph_objects as go
import streamlit as st

DEFAULT_METRICS_DIR = Path("/var/lib/sonda/metrics")

# Well-known DNS resolver IPs → provider name
DNS_PROVIDERS = {}
for _ip in ("8.8.8.8", "8.8.4.4", "2001:4860:4860::8888", "2001:4860:4860::8844"):
    DNS_PROVIDERS[_ip] = "Google"
for _ip in ("1.1.1.1", "1.0.0.1", "2606:4700:4700::1111", "2606:4700:4700::1001"):
    DNS_PROVIDERS[_ip] = "Cloudflare"
for _ip in ("9.9.9.9", "149.112.112.112", "2620:fe::9", "2620:fe::fe"):
    DNS_PROVIDERS[_ip] = "Quad9"

PROVIDER_COLORS = {
    "Cloudflare": "#f48120",
    "Google": "#4285f4",
    "Google (STUN)": "#0f9d58",
    "Quad9": "#e94235",
}
DEFAULT_COLORS = ["#ab63fa", "#19d3f3", "#e6ab02", "#ff6692", "#b6e880"]

OP_COLORS = {
    "connectDone": "#0f9d58",
    "dnsExchangeDone": "#4285f4",
    "tlsHandshakeDone": "#f48120",
    "httpRoundTripDone": "#e94235",
}
OP_LABELS = {
    "connectDone": "TCP Connect",
    "dnsExchangeDone": "DNS Exchange",
    "tlsHandshakeDone": "TLS Handshake",
    "httpRoundTripDone": "HTTP Round Trip",
}
OP_SHORT = {
    "connectDone": "TCP",
    "dnsExchangeDone": "DNS",
    "tlsHandshakeDone": "TLS",
    "httpRoundTripDone": "HTTP",
}
OP_LABELS_INV = {v: k for k, v in OP_LABELS.items()}

OP_INTROS = {
    "tls": (
        "After the TCP connection is established, the browser and server"
        " perform a **TLS handshake** to negotiate encryption. This adds"
        " at least one more round trip (TLS 1.3) or two (TLS 1.2) on top"
        " of the TCP connect latency. The handshake involves exchanging"
        " certificates, verifying them, and agreeing on cryptographic"
        " parameters. Techniques like **connection reuse**, **session"
        " resumption**, and **0-RTT** can amortize this cost."
    ),
    "tcp_connect": (
        "Once the browser has an IP address from DNS, it needs to establish"
        " a TCP connection — a three-way handshake (SYN, SYN-ACK, ACK)."
        " This is essentially a round trip to the server, so the latency"
        " reflects the **network distance** between you and the server."
        " Unlike DNS (which goes to a fixed set of resolvers), TCP connect"
        " latency varies by destination — a CDN edge server nearby will be"
        " fast, while a distant origin server will be slower."
    ),
    "dns": (
        "Before a browser can connect to a server, it must translate the"
        " domain name (e.g., `www.example.com`) into an IP address via a"
        " **DNS exchange**. This lookup can use plain **UDP** (a single"
        " round trip, fast but unencrypted) or **DNS over HTTPS** (private,"
        " but wrapped in TCP + TLS + HTTP, so slower). Provider"
        " infrastructure (Google, Cloudflare, Quad9) and address family"
        " (IPv4 vs IPv6) also influence latency. Note that these operations"
        " are subject to a **timeout** (typically 5 seconds), which is why"
        " some measurements show a duration near that ceiling."
    ),
    "http": (
        "After DNS resolution, TCP connection, and TLS negotiation, the"
        " browser finally sends the **HTTP request** and waits for the"
        " server's first response. This is the actual content exchange —"
        " its duration reflects both network latency (one more round trip)"
        " and server processing time. The charts below also show the"
        " **total** span duration (TCP + TLS + HTTP combined), which"
        " approximates the post-DNS time to first byte."
    ),
}


def span_total_duration(fdf, span_ids, extra_cols=None):
    spans = fdf[fdf["span_id"].isin(set(span_ids))]
    agg = {"t0": ("t0", "min"), "t_end": ("t", "max")}
    if extra_cols:
        for col in extra_cols:
            agg[col] = (col, "first")
    result = spans.groupby("span_id").agg(**agg).reset_index()
    result["duration_ms"] = (
        (result["t_end"] - result["t0"]).dt.total_seconds() * 1000
    )
    return result


def strip_port(addr: str) -> str:
    if addr.startswith("["):
        bracket = addr.rfind("]")
        if bracket != -1:
            return addr[1:bracket]
    colon = addr.rfind(":")
    if colon != -1:
        return addr[:colon]
    return addr


def get_port(addr: str) -> int:
    if addr.startswith("["):
        bracket = addr.rfind("]")
        if bracket != -1 and bracket + 1 < len(addr) and addr[bracket + 1] == ":":
            return int(addr[bracket + 2:])
    colon = addr.rfind(":")
    if colon != -1:
        try:
            return int(addr[colon + 1:])
        except ValueError:
            pass
    return 0


def classify_provider(row) -> str:
    ip = strip_port(row["remote_addr"])
    port = get_port(row["remote_addr"])
    if port == 19302:
        return "Google (STUN)"
    if ip in DNS_PROVIDERS:
        return DNS_PROVIDERS[ip]
    if ip.startswith("142.251.") or ip.startswith("2001:4860:"):
        return "Google"
    if (ip.startswith("104.16.") or ip.startswith("104.20.") or
            ip.startswith("172.66.") or ip.startswith("2606:4700")):
        return "Cloudflare"
    if ip.startswith("149.112.") or ip.startswith("2620:fe"):
        return "Quad9"
    return ip


def classify_measurement(row) -> str:
    port = get_port(row["remote_addr"])
    sp = row.get("server_protocol", "")
    if port == 19302:
        return "STUN"
    if sp == "udp" or (port == 53 and row["protocol"] == "udp"):
        return "DNS over UDP"
    if sp == "doh":
        return "DNS over HTTPS"
    if row["protocol"] == "tcp" and port == 443:
        return "HTTPS"
    return "other"


@st.cache_data
def load_data(metrics_dir: str) -> pd.DataFrame:
    path = Path(metrics_dir)
    files = sorted(path.rglob("*.parquet"))
    if not files:
        st.error(f"No parquet files found in {metrics_dir}")
        st.stop()
    df = pd.concat([pd.read_parquet(f) for f in files], ignore_index=True)
    df["duration_ms"] = df["duration_us"] / 1000.0
    df["provider"] = df.apply(classify_provider, axis=1)
    df["measurement"] = df.apply(classify_measurement, axis=1)
    df["ip"] = df["remote_addr"].apply(strip_port)
    df["port"] = df["remote_addr"].apply(get_port)
    df["addr_family"] = df["ip"].apply(lambda x: "IPv6" if ":" in x else "IPv4")
    df["err_class"] = df["err_class"].fillna("")
    return df


def plot_histogram(df: pd.DataFrame, group_col: str, value_col: str = "duration_ms",
                   title: str = "", xlabel: str = "duration (ms)", bins_per_decade: int = 50):
    fig = go.Figure()
    all_vals = df[value_col].dropna()
    all_vals = all_vals[all_vals > 0]
    if len(all_vals) == 0:
        return fig
    log_min = np.floor(np.log10(all_vals.min()) * bins_per_decade) / bins_per_decade
    log_max = np.ceil(np.log10(all_vals.max()) * bins_per_decade) / bins_per_decade
    bin_edges = 10 ** np.arange(log_min, log_max + 1.0 / bins_per_decade, 1.0 / bins_per_decade)
    bin_centers = np.sqrt(bin_edges[:-1] * bin_edges[1:])
    names = sorted(df[group_col].unique())
    fallback_colors = {}
    fallback_idx = 0
    for name in names:
        base_name = name.split(" (")[0]
        if base_name in PROVIDER_COLORS:
            color = PROVIDER_COLORS[base_name]
        elif base_name in fallback_colors:
            color = fallback_colors[base_name]
        else:
            color = DEFAULT_COLORS[fallback_idx % len(DEFAULT_COLORS)]
            fallback_colors[base_name] = color
            fallback_idx += 1
        dash = "dash" if "(total)" in name or "(HTTPS)" in name else None
        subset = df[df[group_col] == name][value_col].dropna()
        subset = subset[subset > 0]
        if len(subset) == 0:
            continue
        counts, _ = np.histogram(subset, bins=bin_edges)
        freq = counts / counts.sum()
        r, g, b = int(color[1:3], 16), int(color[3:5], 16), int(color[5:7], 16)
        fill_alpha = 0.0 if dash else 0.04
        fig.add_trace(go.Scatter(x=bin_centers, y=freq, mode="lines", name=name,
                                 line=dict(color=color, dash=dash),
                                 fill="tozeroy", fillcolor=f"rgba({r},{g},{b},{fill_alpha})"))
    fig.update_layout(
        xaxis_title=xlabel, yaxis_title="frequency",
        xaxis_type="log", hovermode="x unified",
        xaxis_showgrid=True, yaxis_showgrid=True,
    )
    return fig


def plot_timeseries(df: pd.DataFrame, group_col: str, value_col: str = "duration_ms",
                    title: str = "", ylabel: str = "duration (ms)", resample: str = "1h"):
    fig = go.Figure()
    names = sorted(df[group_col].unique())
    fallback_colors = {}
    fallback_idx = 0
    for name in names:
        base_name = name.split(" (")[0]
        if base_name in PROVIDER_COLORS:
            color = PROVIDER_COLORS[base_name]
        elif base_name in fallback_colors:
            color = fallback_colors[base_name]
        else:
            color = DEFAULT_COLORS[fallback_idx % len(DEFAULT_COLORS)]
            fallback_colors[base_name] = color
            fallback_idx += 1
        dash = "dash" if "(total)" in name else None
        subset = df[df[group_col] == name].set_index("t0")[value_col]
        if len(subset) == 0:
            continue
        median = subset.resample(resample).median().dropna()
        if len(median) == 0:
            continue
        fig.add_trace(go.Scatter(
            x=median.index, y=median.values, mode="lines",
            name=name,
            line=dict(color=color, width=2, dash=dash),
        ))
    fig.update_layout(
        xaxis_title="time", yaxis_title=ylabel,
        xaxis=dict(tickformat="%d %b %H:%M", showgrid=True),
        yaxis_showgrid=True, hovermode="x unified",
    )
    return fig


def build_funnel_sankey(fdf, span_ids, stages):
    span_data = fdf[fdf["span_id"].isin(set(span_ids))]
    if span_data.empty:
        return None
    stage_msgs = [s[0] for s in stages]
    stage_labels = [s[1] for s in stages]
    relevant = span_data[span_data["msg"].isin(stage_msgs)]
    if relevant.empty:
        return None
    pivot = relevant.pivot_table(
        index="span_id", columns="msg", values="err_class", aggfunc="first",
    ).reindex(columns=stage_msgs)

    span_prov = span_data.groupby("span_id")["provider"].first()
    pivot = pivot.join(span_prov)
    providers = sorted(pivot["provider"].dropna().unique())
    nodes = list(providers) + list(stage_labels) + ["Success"]
    node_idx = {n: i for i, n in enumerate(nodes)}
    links = []
    for prov in providers:
        pp = pivot[pivot["provider"] == prov]
        remaining = set(pp.index)
        for i, (msg, label) in enumerate(stages):
            if not remaining:
                break
            has_stage = set(pp.index[pp[msg].notna()]) & remaining
            remaining = has_stage
            if not remaining:
                break
            if i == 0:
                links.append((prov, label, len(remaining), prov))
            stage_results = pp.loc[list(remaining), msg]
            failed_mask = stage_results != ""
            for err_class, count in stage_results[failed_mask].value_counts().items():
                if err_class not in node_idx:
                    nodes.append(err_class)
                    node_idx[err_class] = len(nodes) - 1
                links.append((label, err_class, count, prov))
            survivors = set(stage_results[~failed_mask].index)
            if i == len(stages) - 1:
                if survivors:
                    links.append((label, "Success", len(survivors), prov))
            else:
                if survivors:
                    links.append((label, stages[i + 1][1], len(survivors), prov))
            remaining = survivors
    if not links:
        return None
    sources = [node_idx[s] for s, _, _, _ in links]
    targets = [node_idx[t] for _, t, _, _ in links]
    values = [v for _, _, v, _ in links]
    link_colors = []
    for _, _, _, prov in links:
        c = PROVIDER_COLORS.get(prov, "#999999")
        r, g, b = int(c[1:3], 16), int(c[3:5], 16), int(c[5:7], 16)
        link_colors.append(f"rgba({r},{g},{b},0.4)")
    node_colors = []
    for n in nodes:
        if n in providers:
            node_colors.append(PROVIDER_COLORS.get(n, "#999999"))
        elif n == "Success":
            node_colors.append("rgba(76,175,80,0.7)")
        elif n in stage_labels:
            node_colors.append("rgba(200,200,200,0.5)")
        else:
            node_colors.append("rgba(244,67,54,0.7)")

    fig = go.Figure(go.Sankey(
        textfont=dict(size=14, color="black"),
        node=dict(pad=20, thickness=20, label=nodes, color=node_colors),
        link=dict(source=sources, target=targets, value=values, color=link_colors),
    ))
    fig.update_layout(margin=dict(l=10, r=10, t=30, b=10))
    return fig


def render_waterfall_section(wf, title, key_prefix):
    """Render a waterfall sub-section with table, selector, and chart."""
    st.markdown(f"#### {title}")

    if wf.empty:
        st.info(f"No {title.lower()} spans in the current selection.")
        return

    span_agg = wf.groupby("span_id").agg(
        start_time=("t0", "min"),
        end_time=("t", "max"),
        remote_addr=("remote_addr", "first"),
        provider=("provider", "first"),
        operations=("op_short", lambda x: " + ".join(sorted(x.unique()))),
        has_error=("err_class", lambda x: (x != "").any()),
    ).reset_index()
    span_agg["wall_time_ms"] = (
        (span_agg["end_time"] - span_agg["start_time"])
        .dt.total_seconds() * 1000
    ).round(1)
    total_spans = len(span_agg)
    span_agg = (span_agg.sort_values("start_time", ascending=False)
                .head(500).reset_index(drop=True))

    display_df = span_agg[[
        "start_time", "remote_addr", "provider",
        "operations", "wall_time_ms", "has_error",
    ]].copy()
    display_df.columns = [
        "Time", "Target", "Provider",
        "Operations", "Duration (ms)", "Error",
    ]
    display_df["Time"] = display_df["Time"].dt.strftime("%b %d %H:%M:%S")

    span_labels = [
        f"#{i} {row['Time']} | {row['Target']} | {row['Provider']}"
        f" | {row['Operations']} | {row['Duration (ms)']}ms"
        for i, (_, row) in enumerate(display_df.iterrows())
    ]
    selected_label = st.selectbox(
        "Select a measurement", span_labels,
        key=f"{key_prefix}_select",
    )

    st.caption(
        f"Showing {len(span_agg):,} of {total_spans:,} spans"
        " (most recent first)"
    )
    event = st.dataframe(
        display_df,
        selection_mode="single-row",
        on_select="rerun",
        width="stretch",
        hide_index=True,
        key=f"{key_prefix}_table",
    )

    selected_idx = 0
    if event.selection.rows:
        selected_idx = event.selection.rows[0]
    else:
        selected_idx = span_labels.index(selected_label)

    selected_span_id = span_agg.iloc[selected_idx]["span_id"]
    span_df = wf[wf["span_id"] == selected_span_id].sort_values("t0")
    span_start = span_df["t0"].min()

    fig = go.Figure()
    for _, row in span_df.iterrows():
        start_ms = (row["t0"] - span_start).total_seconds() * 1000
        dur_ms = row["duration_us"] / 1000
        label = row["op_label"]
        if row["msg"] == "connectDone" and row["protocol"] == "udp":
            color = "#19d3f3"
        else:
            color = OP_COLORS.get(row["msg"], "#999999")
        fig.add_trace(go.Bar(
            y=[label],
            x=[dur_ms],
            base=[start_ms],
            orientation="h",
            name=label,
            marker_color=color,
            text=[f"{dur_ms:.1f}ms"],
            textposition="auto",
            showlegend=False,
            hovertemplate=(
                f"{label}<br>"
                f"Start: {start_ms:.1f}ms<br>"
                f"Duration: {dur_ms:.1f}ms<br>"
                f"End: {start_ms + dur_ms:.1f}ms"
                "<extra></extra>"
            ),
        ))
    total_ms = (span_df["t"].max() - span_start).total_seconds() * 1000
    fig.add_trace(go.Bar(
        y=["Total"],
        x=[total_ms],
        base=[0],
        orientation="h",
        name="Total",
        marker_color="#888888",
        text=[f"{total_ms:.1f}ms"],
        textposition="auto",
        showlegend=False,
        hovertemplate=(
            f"Total<br>"
            f"Duration: {total_ms:.1f}ms"
            "<extra></extra>"
        ),
    ))
    fig.update_layout(
        xaxis_title="time since start (ms)",
        barmode="overlay",
        height=max(250, (len(span_df) + 1) * 80 + 100),
        xaxis_showgrid=True,
        yaxis_showgrid=True,
        yaxis_autorange="reversed",
    )
    st.plotly_chart(fig, width="stretch")

    st.caption("Operation details")
    details = []
    for _, row in span_df.iterrows():
        start_ms = (row["t0"] - span_start).total_seconds() * 1000
        dur_ms = row["duration_us"] / 1000
        details.append({
            "Operation": row["op_label"],
            "Start (ms)": round(start_ms, 1),
            "Duration (ms)": round(dur_ms, 1),
            "End (ms)": round(start_ms + dur_ms, 1),
            "Target": row["remote_addr"],
            "Protocol": row["protocol"],
            "Error": row["err_class"] if row["err_class"] else "",
        })
    st.dataframe(
        pd.DataFrame(details),
        width="stretch",
        hide_index=True,
    )


OP_VIEWS = [
    ("dns", "DNS Latency", lambda df: df[df["msg"] == "dnsExchangeDone"]),
    ("tcp_connect", "TCP Connect", lambda df: df[(df["msg"] == "connectDone") & (df["protocol"] == "tcp")]),
    ("tls", "TLS Handshake", lambda df: df[df["msg"] == "tlsHandshakeDone"]),
    ("http", "HTTP Round Trip", lambda df: df[df["msg"] == "httpRoundTripDone"]),
]


def render_overview_tab(df):
    st.markdown(
        "[Sonda](https://github.com/bassosimone/sonda) is a prototype"
        " personal tool that continuously measures the time required to"
        " establish web-like connections and exchange data with well-known"
        " services (DNS resolvers, HTTPS endpoints). It has been running"
        " across several networks over time; this dashboard provides a"
        " continuously updated overview of the collected data"
        " as read from `/var/lib/sonda/metrics`."
    )

    st.divider()

    vp_records = []
    for col, family in [("reflexive_addr_v4", "IPv4"), ("reflexive_addr_v6", "IPv6")]:
        col_rows = df[df[col].notna()]
        for ip in sorted(col_rows[col].unique()):
            ip_rows = col_rows[col_rows[col] == ip]
            vp_records.append({
                "Public IP": f"https://ipinfo.io/{ip}",
                "Family": family,
                "First seen": ip_rows["t0"].min().strftime("%b %d, %Y"),
                "Last seen": ip_rows["t0"].max().strftime("%b %d, %Y"),
                "Observations": f"{len(ip_rows):,}",
            })
    if vp_records:
        st.subheader("Vantage points")
        st.dataframe(
            pd.DataFrame(vp_records),
            column_config={
                "Public IP": st.column_config.LinkColumn(
                    "Public IP", display_text=r"https://ipinfo\.io/(.*)",
                ),
            },
            width="stretch",
            hide_index=True,
        )
        st.divider()

    st.subheader("Row counts")
    row_counts = []
    for key, label, fn in OP_VIEWS:
        view_df = fn(df)
        if not view_df.empty:
            for prov, count in view_df["provider"].value_counts().items():
                row_counts.append({"operation": label, "provider": prov, "count": count})
    if row_counts:
        rc_df = pd.DataFrame(row_counts).sort_values("count", ascending=False)
        st.dataframe(rc_df, width="stretch", hide_index=True)

    st.divider()
    st.subheader("Schema")
    schema_df = pd.DataFrame({"column": df.dtypes.index, "dtype": df.dtypes.values.astype(str)})
    st.dataframe(schema_df, width="stretch", hide_index=True)

    st.divider()
    st.subheader("Raw data sample")
    st.dataframe(df.head(500), width="stretch")

    config_path = Path("/etc/sonda/scan/default.yml")
    if config_path.is_file():
        config_text = config_path.read_text()
        if config_text.strip():
            st.divider()
            st.subheader("Configuration")
            st.code(config_text, language="yaml")


def render_waterfall_tab(fdf):
    st.markdown(
        "Visiting a webpage requires several preliminary operations before"
        " content can be received (or sent). The browser must first"
        " **resolve the domain name** to an IP address via a DNS exchange,"
        " then **establish a connection** to the server (TCP handshake,"
        " TLS handshake, and HTTP round trip). Techniques such as DNS"
        " caching, connection reuse, and session resumption reduce the"
        " impact of these operations in practice, but each new destination"
        " pays the full cost. The two sections below show these phases"
        " for individual measurements."
    )

    wf = fdf.copy()
    wf["op_label"] = wf["msg"].map(OP_LABELS).fillna(wf["msg"])
    wf["op_short"] = wf["msg"].map(OP_SHORT).fillna(wf["msg"])
    connect_mask = wf["msg"] == "connectDone"
    wf.loc[connect_mask, "op_label"] = (
        wf.loc[connect_mask, "protocol"].str.upper() + " Connect"
    )
    wf.loc[connect_mask, "op_short"] = (
        wf.loc[connect_mask, "protocol"].str.upper()
    )

    dns_span_ids = set(wf.loc[wf["msg"] == "dnsExchangeDone", "span_id"])

    render_waterfall_section(
        wf[wf["span_id"].isin(dns_span_ids)],
        "DNS Exchange",
        "wf_dns",
    )

    st.divider()

    render_waterfall_section(
        wf[~wf["span_id"].isin(dns_span_ids)],
        "Connection Establishment",
        "wf_conn",
    )


def render_operation_tab(fdf, key, label, op_df):
    if key in OP_INTROS:
        st.markdown(OP_INTROS[key])
        st.divider()

    if key == "dns":
        dns_total = span_total_duration(
            fdf, op_df["span_id"], extra_cols=["provider", "addr_family"],
        )
        dns_meas = op_df[["span_id", "measurement"]].drop_duplicates("span_id")
        dns_total = dns_total.merge(dns_meas, on="span_id", how="left")

        udp_exchange = op_df[op_df["measurement"] == "DNS over UDP"]
        st.subheader("DNS over UDP — exchange latency over time (hourly p50)")
        fig = plot_timeseries(udp_exchange, "provider")
        st.plotly_chart(fig, width="stretch")

        st.divider()
        doh_exchange = op_df[op_df["measurement"] == "DNS over HTTPS"].copy()
        doh_exchange["provider"] = doh_exchange["provider"] + " (exchange)"
        doh_total = dns_total[dns_total["measurement"] == "DNS over HTTPS"].copy()
        doh_total["provider"] = doh_total["provider"] + " (total)"
        doh_combined = pd.concat([doh_exchange, doh_total], ignore_index=True)
        st.subheader("DNS over HTTPS — exchange vs total latency over time (hourly p50)")
        fig = plot_timeseries(doh_combined, "provider")
        st.plotly_chart(fig, width="stretch")

        st.divider()
        st.subheader("DNS exchange distribution — UDP vs HTTPS by provider")
        udp_ex = op_df[op_df["measurement"] == "DNS over UDP"].copy()
        udp_ex["provider_proto"] = udp_ex["provider"] + " (UDP)"
        https_ex = op_df[op_df["measurement"] == "DNS over HTTPS"].copy()
        https_ex["provider_proto"] = https_ex["provider"] + " (HTTPS)"
        proto_combined = pd.concat([udp_ex, https_ex], ignore_index=True)
        fig = plot_histogram(proto_combined, "provider_proto")
        st.plotly_chart(fig, width="stretch")

        st.divider()
        st.subheader("DNS over HTTPS — exchange vs total latency distribution by provider")
        doh_ex = op_df[op_df["measurement"] == "DNS over HTTPS"].copy()
        doh_ex["provider_metric"] = doh_ex["provider"] + " (exchange)"
        doh_tot = dns_total[dns_total["measurement"] == "DNS over HTTPS"].copy()
        doh_tot["provider_metric"] = doh_tot["provider"] + " (total)"
        doh_dist = pd.concat([doh_ex, doh_tot], ignore_index=True)
        fig = plot_histogram(doh_dist, "provider_metric")
        st.plotly_chart(fig, width="stretch")

    elif key == "http":
        http_total = span_total_duration(
            fdf, op_df["span_id"], extra_cols=["provider", "addr_family"],
        )

        http_exchange = op_df.copy()
        http_exchange["provider"] = http_exchange["provider"] + " (round trip)"
        http_tot = http_total.copy()
        http_tot["provider"] = http_tot["provider"] + " (total)"
        http_combined = pd.concat([http_exchange, http_tot], ignore_index=True)

        st.subheader("HTTP round trip vs total latency over time (hourly p50)")
        fig = plot_timeseries(http_combined, "provider")
        st.plotly_chart(fig, width="stretch")

        st.divider()
        st.subheader("HTTP round trip vs total latency distribution by provider")
        http_ex_dist = op_df.copy()
        http_ex_dist["provider_metric"] = http_ex_dist["provider"] + " (round trip)"
        http_tot_dist = http_total.copy()
        http_tot_dist["provider_metric"] = http_tot_dist["provider"] + " (total)"
        http_dist = pd.concat([http_ex_dist, http_tot_dist], ignore_index=True)
        fig = plot_histogram(http_dist, "provider_metric")
        st.plotly_chart(fig, width="stretch")

        tcp_df = fdf[(fdf["msg"] == "connectDone") & (fdf["protocol"] == "tcp")]
        tcp_by_span = tcp_df.set_index("span_id")["duration_us"]
        http_by_span = op_df.set_index("span_id")[["duration_us", "provider"]]
        common = http_by_span.join(tcp_by_span, rsuffix="_tcp").dropna()
        common["http_ratio"] = common["duration_us"] / common["duration_us_tcp"]
        common = common[common["http_ratio"] > 0].reset_index()

        total_by_span = http_total.set_index("span_id")[["duration_ms"]]
        tcp_ms = tcp_df.set_index("span_id")["duration_us"] / 1000
        common_total = total_by_span.join(tcp_ms).dropna()
        common_total.columns = ["total_ms", "tcp_ms"]
        common_total["total_ratio"] = common_total["total_ms"] / common_total["tcp_ms"]
        common_total = common_total[common_total["total_ratio"] > 0]

        ratio_df = common[["span_id", "provider", "http_ratio"]].merge(
            common_total[["total_ratio"]],
            left_on="span_id", right_index=True, how="inner",
        )
        if not ratio_df.empty:
            rt = ratio_df.copy()
            rt["provider"] = rt["provider"] + " (round trip)"
            rt = rt.rename(columns={"http_ratio": "ratio"})
            tot = ratio_df.copy()
            tot["provider"] = tot["provider"] + " (total)"
            tot = tot.rename(columns={"total_ratio": "ratio"})
            combined_ratio = pd.concat([
                rt[["provider", "ratio"]],
                tot[["provider", "ratio"]],
            ], ignore_index=True)
            st.divider()
            st.subheader("Duration / TCP connect ratio by provider")
            fig = plot_histogram(
                combined_ratio, "provider",
                value_col="ratio",
                xlabel="duration / TCP connect duration (in round trips)",
            )
            st.plotly_chart(fig, width="stretch")

    else:
        st.subheader(f"{label} latency over time (hourly p50)")
        fig = plot_timeseries(op_df, "provider")
        st.plotly_chart(fig, width="stretch")

        st.divider()
        st.subheader(f"{label} latency distribution by provider")
        fig = plot_histogram(op_df, "provider")
        st.plotly_chart(fig, width="stretch")

        if key == "tls":
            tcp_df = fdf[(fdf["msg"] == "connectDone") & (fdf["protocol"] == "tcp")]
            tcp_by_span = tcp_df.set_index("span_id")["duration_us"]
            tls_by_span = op_df.set_index("span_id")[["duration_us", "provider"]]
            common = tls_by_span.join(tcp_by_span, rsuffix="_tcp").dropna()
            common["ratio"] = common["duration_us"] / common["duration_us_tcp"]
            common = common[common["ratio"] > 0].reset_index()
            if not common.empty:
                st.divider()
                st.subheader("TLS / TCP duration ratio by provider")
                fig = plot_histogram(
                    common, "provider",
                    value_col="ratio",
                    xlabel="TLS handshake / TCP connect duration",
                )
                st.plotly_chart(fig, width="stretch")


def render_ttfb_tab(fdf):
    st.markdown(
        "This tab estimates the **Time to First Byte (TTFB)** by composing"
        " observed latencies from individual phases: DNS resolution and"
        " connection establishment (TCP + TLS + HTTP). Since sonda does not"
        " yet link DNS lookups to their downstream connections via trace IDs,"
        " we cannot measure TTFB directly. Instead, we use a **Monte Carlo"
        " approach**: we randomly pair DNS duration samples with connection"
        " duration samples and sum them. With enough samples, temporal"
        " correlations between phases average out, producing a reliable"
        " estimate of the expected TTFB distribution for a given combination"
        " of DNS resolver and target endpoint. This estimate assumes"
        " independence between phases and does not account for DNS caching,"
        " connection reuse, or other optimizations that reduce real-world TTFB."
    )
    st.divider()

    dns_df = fdf[fdf["msg"] == "dnsExchangeDone"]
    http_df = fdf[fdf["msg"] == "httpRoundTripDone"]

    if dns_df.empty or http_df.empty:
        st.warning("Need both DNS and HTTP data to estimate TTFB.")
        return

    dns_providers = sorted(dns_df["provider"].unique())
    dns_protocols = sorted(dns_df["measurement"].unique())
    target_providers = sorted(http_df["provider"].unique())

    st.subheader("Estimated TTFB distribution")

    col1, col2, col3 = st.columns(3)
    with col1:
        sel_dns_provider = st.selectbox(
            "DNS resolver", dns_providers, key="ttfb_dns_provider",
        )
    with col2:
        sel_dns_protocol = st.selectbox(
            "DNS protocol", dns_protocols, key="ttfb_dns_protocol",
        )
    with col3:
        sel_target = st.selectbox(
            "Target", target_providers, key="ttfb_target",
        )

    dns_for_provider = dns_df[
        (dns_df["provider"] == sel_dns_provider) &
        (dns_df["measurement"] == sel_dns_protocol)
    ]
    if sel_dns_protocol == "DNS over UDP":
        dns_samples = dns_for_provider["duration_ms"].dropna().values
    else:
        doh_total = span_total_duration(fdf, dns_for_provider["span_id"])
        dns_samples = doh_total["duration_ms"].dropna().values

    target_http = http_df[http_df["provider"] == sel_target]
    conn_total = span_total_duration(fdf, target_http["span_id"])
    conn_samples = conn_total["duration_ms"].dropna().values

    if len(dns_samples) == 0 or len(conn_samples) == 0:
        st.warning(
            "No data for the selected combination of DNS resolver,"
            " protocol, and target."
        )
        return

    rng = np.random.default_rng(42)
    n = 10_000
    dns_s = rng.choice(dns_samples, size=n, replace=True)
    conn_s = rng.choice(conn_samples, size=n, replace=True)
    ttfb = dns_s + conn_s

    ttfb_df = pd.DataFrame({"duration_ms": ttfb, "component": "Estimated TTFB"})
    dns_comp = pd.DataFrame({"duration_ms": dns_samples, "component": "DNS"})
    conn_comp = pd.DataFrame({"duration_ms": conn_samples, "component": "Connection"})
    all_components = pd.concat(
        [dns_comp, conn_comp, ttfb_df], ignore_index=True,
    )

    fig = plot_histogram(all_components, "component")
    st.plotly_chart(fig, width="stretch")

    st.caption(
        f"Monte Carlo simulation: {n:,} random pairings of"
        f" {len(dns_samples):,} DNS samples and"
        f" {len(conn_samples):,} connection samples."
    )


def render_errors_tab(fdf):
    st.markdown(
        "Network measurements inevitably encounter errors. Some are"
        " **transient** — a DNS timeout, a brief routing glitch — and"
        " average out over time. Others are **structural**: an address"
        " family that is not available, a TLS certificate that does not"
        " match, a target that is persistently unreachable. Both kinds"
        " are informative."
        "\n\n"
        "The latency distributions in the other tabs only include"
        " **successful** measurements. When a large fraction of"
        " connection attempts fail — as often happens with IPv6 in"
        " networks that do not fully support it — those tabs show a"
        " survivor-biased view. This tab provides the complementary"
        " picture: how many measurements fail, where in the connection"
        " pipeline they fail, and whether the failures are random or"
        " systematic."
        "\n\n"
        "Errors at different stages carry different meaning."
        " A **TCP** failure means the network path itself is broken or"
        " the address family is unavailable."
        " A **TLS** failure means the network works but the endpoint is"
        " misconfigured."
        " An **HTTP** failure means everything underneath works but the"
        " application layer has a problem."
        " The survival funnels below make this structure visible."
    )
    st.divider()

    errs = fdf[fdf["err_class"] != ""]
    if errs.empty:
        st.success("No errors in current selection.")
        return

    st.subheader("Error distribution")
    total_events = len(fdf)
    errs_by_phase = errs.copy()
    errs_by_phase["phase"] = errs_by_phase["msg"].map(OP_LABELS).fillna(
        errs_by_phase["msg"]
    )
    bar_data = (
        errs_by_phase.groupby(["err_class", "phase"])
        .size()
        .reset_index(name="count")
    )
    bar_data["pct"] = bar_data["count"] / total_events * 100
    fig = go.Figure()
    for phase in sorted(bar_data["phase"].unique()):
        subset = bar_data[bar_data["phase"] == phase]
        color = OP_COLORS.get(OP_LABELS_INV.get(phase, ""), "#999999")
        fig.add_trace(go.Bar(
            y=subset["err_class"],
            x=subset["pct"],
            name=phase,
            orientation="h",
            marker_color=color,
            hovertemplate="%{y}: %{x:.1f}%<extra>" + phase + "</extra>",
        ))
    fig.update_layout(
        barmode="stack",
        xaxis_title="% of all events",
        yaxis_title="",
        xaxis_showgrid=True,
        yaxis_showgrid=False,
        legend=dict(orientation="h", yanchor="bottom", y=1.02),
    )
    st.plotly_chart(fig, width="stretch")

    st.divider()
    st.subheader("Survival funnel")

    doh_span_ids = set(
        fdf[fdf["measurement"] == "DNS over HTTPS"]["span_id"].unique()
    )
    funnels = [
        (
            "DNS over UDP",
            fdf[fdf["measurement"] == "DNS over UDP"]["span_id"].unique(),
            [
                ("connectDone", "Connect"),
                ("dnsExchangeDone", "DNS Exchange"),
            ],
        ),
        (
            "DNS over HTTPS",
            list(doh_span_ids),
            [
                ("connectDone", "TCP Connect"),
                ("tlsHandshakeDone", "TLS Handshake"),
                ("dnsExchangeDone", "DNS Exchange"),
            ],
        ),
        (
            "HTTPS",
            [
                s for s in
                fdf[fdf["measurement"] == "HTTPS"]["span_id"].unique()
                if s not in doh_span_ids
            ],
            [
                ("connectDone", "TCP Connect"),
                ("tlsHandshakeDone", "TLS Handshake"),
                ("httpRoundTripDone", "HTTP Round Trip"),
            ],
        ),
    ]
    for funnel_title, span_ids, stages in funnels:
        if len(span_ids) == 0:
            continue
        fig = build_funnel_sankey(fdf, span_ids, stages)
        if fig is not None:
            st.markdown(f"**{funnel_title}** ({len(span_ids):,} spans)")
            st.plotly_chart(fig, width="stretch")

    st.divider()
    st.subheader("Error rate over time")
    errs_ts = fdf.set_index("t0").copy()
    errs_ts["phase"] = errs_ts["msg"].map(OP_LABELS).fillna(errs_ts["msg"])
    errs_ts["is_error"] = errs_ts["err_class"] != ""
    fig = go.Figure()
    for phase in sorted(errs_ts["phase"].unique()):
        subset = errs_ts[errs_ts["phase"] == phase]["is_error"]
        hourly = subset.resample("1h").mean() * 100
        hourly = hourly.dropna()
        if hourly.empty:
            continue
        color = OP_COLORS.get(OP_LABELS_INV.get(phase, ""), "#999999")
        fig.add_trace(go.Scatter(
            x=hourly.index, y=hourly.values, mode="lines",
            name=phase, line=dict(color=color, width=2),
        ))
    fig.update_layout(
        xaxis_title="time", yaxis_title="error rate (%)",
        xaxis=dict(tickformat="%d %b %H:%M", showgrid=True),
        yaxis_showgrid=True, hovermode="x unified",
    )
    st.plotly_chart(fig, width="stretch")


def main():
    st.set_page_config(page_title="Sonda Metrics Explorer", layout="wide")
    st.title("Sonda Metrics Explorer")

    metrics_dir = sys.argv[1] if len(sys.argv) > 1 else str(DEFAULT_METRICS_DIR)
    df = load_data(metrics_dir)

    st.sidebar.header("Filters")

    measurements = sorted(df["measurement"].unique())
    sel_measurements = st.sidebar.multiselect("Measurement type", measurements, default=measurements)

    providers = sorted(df["provider"].unique())
    sel_providers = st.sidebar.multiselect("Provider", providers, default=providers)

    events = sorted(df["msg"].unique())
    sel_events = st.sidebar.multiselect("Event type", events, default=events)

    addr_families = sorted(df["addr_family"].unique())
    sel_families = st.sidebar.multiselect("Address family", addr_families, default=addr_families)

    vantage_ips = set()
    for col in ("reflexive_addr_v4", "reflexive_addr_v6"):
        if col in df.columns:
            vantage_ips.update(df[col].dropna().unique())
    vantage_ips = sorted(vantage_ips)
    sel_vantage = st.sidebar.multiselect("Vantage point", vantage_ips, default=vantage_ips)

    err_classes_raw = sorted(df["err_class"].unique())
    err_options = ["(success)"] + [e for e in err_classes_raw if e != ""]
    sel_err = st.sidebar.multiselect("Error class", err_options, default=err_options)

    date_min = df["t0"].min().date()
    date_max = df["t0"].max().date()
    default_start = max(date_min, date_max - datetime.timedelta(days=7))
    sel_dates = st.sidebar.date_input("Date range", value=(default_start, date_max),
                                       min_value=date_min, max_value=date_max)

    sel_err_mapped = set()
    for e in sel_err:
        if e == "(success)":
            sel_err_mapped.add("")
        else:
            sel_err_mapped.add(e)
    sel_vantage_set = set(sel_vantage)
    vantage_mask = pd.Series(False, index=df.index)
    for col in ("reflexive_addr_v4", "reflexive_addr_v6"):
        if col in df.columns:
            vantage_mask = vantage_mask | df[col].isin(sel_vantage_set)
    mask = (
        vantage_mask &
        df["measurement"].isin(sel_measurements) &
        df["provider"].isin(sel_providers) &
        df["msg"].isin(sel_events) &
        df["addr_family"].isin(sel_families) &
        df["err_class"].isin(sel_err_mapped)
    )
    if len(sel_dates) == 2:
        mask = mask & (df["t0"].dt.date >= sel_dates[0]) & (df["t0"].dt.date <= sel_dates[1])
    fdf = df[mask]

    st.sidebar.markdown(f"**{len(fdf):,}** / {len(df):,} rows selected")

    if fdf.empty:
        st.warning("No data matches the current filters.")
        return

    active_views = [(key, label, fn) for key, label, fn in OP_VIEWS if not fn(fdf).empty]
    tab_labels = ["Overview", "Connection Waterfall"] + [label for _, label, _ in active_views] + ["Estimated TTFB", "Errors"]
    tabs = st.tabs(tab_labels)
    tab_idx = 0

    with tabs[tab_idx]:
        tab_idx += 1
        render_overview_tab(df)

    with tabs[tab_idx]:
        tab_idx += 1
        render_waterfall_tab(fdf)

    for key, label, fn in active_views:
        with tabs[tab_idx]:
            tab_idx += 1
            render_operation_tab(fdf, key, label, fn(fdf))

    with tabs[tab_idx]:
        tab_idx += 1
        render_ttfb_tab(fdf)

    with tabs[tab_idx]:
        tab_idx += 1
        render_errors_tab(fdf)


if __name__ == "__main__":
    main()
