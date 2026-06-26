// SPDX-License-Identifier: GPL-3.0-or-later

package structured

// Metrics is a Parquet row extracted from a *Done structured log event.
// Each row represents a single completed operation (connect, TLS handshake,
// HTTP round trip, or DNS exchange). Fields that are only meaningful for
// a subset of event types are nullable (pointer types).
type Metrics struct {
	SpanID                 string  `parquet:"span_id"`
	Msg                    string  `parquet:"msg"`
	T0                     int64   `parquet:"t0,timestamp(microsecond)"`
	T                      int64   `parquet:"t,timestamp(microsecond)"`
	DurationUs             int64   `parquet:"duration_us"`
	LocalAddr              string  `parquet:"local_addr"`
	RemoteAddr             string  `parquet:"remote_addr"`
	Protocol               string  `parquet:"protocol"`
	ErrClass               *string `parquet:"err_class,optional"`
	ServerProtocol         *string `parquet:"server_protocol,optional"`
	HTTPResponseStatusCode *int64  `parquet:"http_response_status_code,optional"`
	ReflexiveAddrV4        *string `parquet:"reflexive_addr_v4,optional"`
	ReflexiveAddrV6        *string `parquet:"reflexive_addr_v6,optional"`
}
