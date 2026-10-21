// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"maps"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// metricsSet contains the Prometheus metrics we export.
type metricsSet struct {
	// Spans whose `stdout.txt` entity we read.
	Spans *prometheus.CounterVec

	// Duration histogram for successful stages.
	ConnectSeconds        *prometheus.HistogramVec
	DNSExchangeSeconds    *prometheus.HistogramVec
	HTTPBodyStreamSeconds *prometheus.HistogramVec
	HTTPRoundTripSeconds  *prometheus.HistogramVec
	TLSHandshakeSeconds   *prometheus.HistogramVec

	// Bytes read and written by spans.
	IOBytes *prometheus.CounterVec

	// Average speed histo based on events between connectDone and closeStart.
	ConnAvgSpeeds *prometheus.HistogramVec
}

// metricsStageLabels are the labels of [*metricsSet] histograms.
var metricsStageLabels = []string{
	"dns_query_name",
	"dns_query_type",
	"http_request_url",
	"protocol",
	"reflexive_addr_v4",
	"reflexive_addr_v6",
	"remote_addr",
	"tls_server_name",
}

// metricsSpanLabels are the labels of [*metricsSet.Spans].
var metricsSpanLabels = append([]string{
	"err_class",
	"failed_at",
	"http_status",
}, metricsStageLabels...)

// metricsIOLabels are the labels of [*metricsSet.IOBytes].
var metricsIOLabels = append([]string{"op"}, metricsStageLabels...)

// metricsTimeBuckets are the histogram buckets in seconds.
var metricsTimeBuckets = prometheus.ExponentialBuckets(0.001, 2, 15)

// metricsSpeedBuckets are the histogram buckets (bit/s) using the
// Renard R10 series (ISO 3): 10 buckets per decade, ~26% apart.
var metricsSpeedBuckets = []float64{
	1e05, 1.25e05, 1.6e05, 2e05, 2.5e05, 3.15e05, 4e05, 5e05, 6.3e05, 8e05, // 100 kbit/s
	1e06, 1.25e06, 1.6e06, 2e06, 2.5e06, 3.15e06, 4e06, 5e06, 6.3e06, 8e06, // Mbit/s
	1e07, 1.25e07, 1.6e07, 2e07, 2.5e07, 3.15e07, 4e07, 5e07, 6.3e07, 8e07,
	1e08, 1.25e08, 1.6e08, 2e08, 2.5e08, 3.15e08, 4e08, 5e08, 6.3e08, 8e08,
	1e09, 1.25e09, 1.6e09, 2e09, 2.5e09, 3.15e09, 4e09, 5e09, 6.3e09, 8e09, // Gbit/s
	1e10,
}

// newMetricsSet creates a [*metricsSet] registered into reg.
func newMetricsSet(reg prometheus.Registerer) *metricsSet {
	f := promauto.With(reg)
	return &metricsSet{
		Spans: f.NewCounterVec(prometheus.CounterOpts{
			Name: "sonda_ptnop_spans_total",
			Help: "Number of ptnop spans processed.",
		}, metricsSpanLabels),

		ConnectSeconds: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "sonda_ptnop_connect_seconds",
			Help:    "Duration of successful connects.",
			Buckets: metricsTimeBuckets,
		}, metricsStageLabels),

		DNSExchangeSeconds: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "sonda_ptnop_dns_exchange_seconds",
			Help:    "Duration of successful DNS exchanges.",
			Buckets: metricsTimeBuckets,
		}, metricsStageLabels),

		HTTPBodyStreamSeconds: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "sonda_ptnop_http_body_stream_seconds",
			Help:    "Duration of successful HTTP body streams.",
			Buckets: metricsTimeBuckets,
		}, metricsStageLabels),

		HTTPRoundTripSeconds: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "sonda_ptnop_http_round_trip_seconds",
			Help:    "Duration of successful HTTP round trips.",
			Buckets: metricsTimeBuckets,
		}, metricsStageLabels),

		TLSHandshakeSeconds: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "sonda_ptnop_tls_handshake_seconds",
			Help:    "Duration of successful TLS handshakes.",
			Buckets: metricsTimeBuckets,
		}, metricsStageLabels),

		IOBytes: f.NewCounterVec(prometheus.CounterOpts{
			Name: "sonda_ptnop_io_bytes_total",
			Help: "Application-layer bytes read and written on ptnop conns.",
		}, metricsIOLabels),

		ConnAvgSpeeds: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "sonda_ptnop_conn_avg_speed_bits_per_second",
			Help:    "Average speed in bit/s between connectDone and closeStart.",
			Buckets: metricsSpeedBuckets,
		}, metricsIOLabels),
	}
}

// Observe updates the metrics using the given unified event.
func (ms *metricsSet) Observe(uue *updateUnifiedEvent) {
	// 1. Count the span along with its outcome.
	stage := prometheus.Labels{
		"dns_query_name":    uue.DNSQueryName,
		"dns_query_type":    uue.DNSQueryType,
		"http_request_url":  uue.HTTPRequestUrl,
		"protocol":          uue.Protocol,
		"reflexive_addr_v4": uue.ReflexiveAddrV4,
		"reflexive_addr_v6": uue.ReflexiveAddrV6,
		"remote_addr":       uue.RemoteAddr,
		"tls_server_name":   uue.TLSServerName,
	}

	span := prometheus.Labels{
		"err_class":   uue.FailedErr,
		"failed_at":   "none",
		"http_status": "",
	}
	if uue.FailedAt != "" {
		span["failed_at"] = uue.FailedAt
	}

	if uue.HTTPResponseStatusCode > 0 {
		span["http_status"] = strconv.Itoa(uue.HTTPResponseStatusCode)
	}

	maps.Copy(span, stage)
	ms.Spans.With(span).Inc()

	// 2. Observe the duration of each successful stage.
	metricsObserveStageDuration(
		ms.ConnectSeconds, stage, uue.ConnectDuration, uue.ConnectErr)

	metricsObserveStageDuration(
		ms.DNSExchangeSeconds, stage, uue.DNSExchangeDuration, uue.DNSExchangeErr)

	metricsObserveStageDuration(
		ms.HTTPBodyStreamSeconds, stage, uue.HTTPBodyStreamDuration, uue.HTTPBodyStreamErr)

	metricsObserveStageDuration(
		ms.HTTPRoundTripSeconds, stage, uue.HTTPRoundTripDuration, uue.HTTPRoundTripErr)

	metricsObserveStageDuration(
		ms.TLSHandshakeSeconds, stage, uue.TLSHandshakeDuration, uue.TLSHandshakeErr)

	// 3. Count the bytes, skipping zero-bytes entries to avoid creating series for
	// spans that never exchanged data (e.g., a failed TCP connect).
	metricsAddIOBytes(ms.IOBytes, stage, "read", uue.ReadBytesTot)

	metricsAddIOBytes(ms.IOBytes, stage, "write", uue.WriteBytesTot)

	// 4. Measure average speed since connectDone until closeStart
	metricsObserveConnAverageSpeed(
		ms.ConnAvgSpeeds, uue.ConnLifetime, uue.ReadBytesTot, "read", stage)

	metricsObserveConnAverageSpeed(
		ms.ConnAvgSpeeds, uue.ConnLifetime, uue.WriteBytesTot, "write", stage)
}

func metricsAddIOBytes(cv *prometheus.CounterVec, stage prometheus.Labels, op string, count int64) {
	if count > 0 {
		labels := prometheus.Labels{"op": op}
		maps.Copy(labels, stage)
		cv.With(labels).Add(float64(count))
	}
}

// metricsObserveStageDuration observes the duration of a stage if it succeeded.
//
// A stage that did not run has zero duration. A stage that failed or
// was skipped has a nonempty errClass. We also skip durations <= 0
// because t and t0 are wall-clock times: a clock step during the stage
// could make the difference zero or negative.
func metricsObserveStageDuration(
	hv *prometheus.HistogramVec, labels prometheus.Labels, d time.Duration, errClass string) {
	if d > 0 && errClass == "" {
		hv.With(labels).Observe(d.Seconds())
	}
}

// metricsObserveConnAverageSpeed observes average speeds.
//
// We skip connections with zero lifetime or zero transferred bytes.
func metricsObserveConnAverageSpeed(
	hv *prometheus.HistogramVec,
	lifetime time.Duration,
	numBytes int64,
	operation string,
	stage prometheus.Labels,
) {
	if lifetime > 0 && numBytes > 0 {
		bitsec := float64(8*numBytes) / lifetime.Seconds()
		labels := prometheus.Labels{"op": operation}
		maps.Copy(labels, stage)
		hv.With(labels).Observe(bitsec)
	}
}
