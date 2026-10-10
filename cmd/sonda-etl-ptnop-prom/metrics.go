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
	// Spans whose `stdout.txt` we read.
	Spans *prometheus.CounterVec

	// Duration histogram for successful stages.
	ConnectSeconds        *prometheus.HistogramVec
	DNSExchangeSeconds    *prometheus.HistogramVec
	HTTPBodyStreamSeconds *prometheus.HistogramVec
	HTTPRoundTripSeconds  *prometheus.HistogramVec
	TLSHandshakeSeconds   *prometheus.HistogramVec

	// Bytes read and written by spans.
	IOBytes *prometheus.CounterVec
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

// metricsBuckets are the histogram buckets in seconds: 15 exponential buckets
// from 1 ms to 16.384 s (1ms * 2^14), plus the implicit +Inf.
var metricsBuckets = prometheus.ExponentialBuckets(0.001, 2, 15)

// newMetricsSet creates a [*metricsSet] registered into reg.
func newMetricsSet(reg prometheus.Registerer) *metricsSet {
	f := promauto.With(reg)
	newHistogram := func(name, help string) *prometheus.HistogramVec {
		return f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    name,
			Help:    help,
			Buckets: metricsBuckets,
		}, metricsStageLabels)
	}

	return &metricsSet{
		Spans: f.NewCounterVec(prometheus.CounterOpts{
			Name: "sonda_ptnop_spans_total",
			Help: "Number of ptnop spans processed.",
		}, metricsSpanLabels),

		ConnectSeconds: newHistogram(
			"sonda_ptnop_connect_seconds",
			"Duration of successful connects.",
		),

		DNSExchangeSeconds: newHistogram(
			"sonda_ptnop_dns_exchange_seconds",
			"Duration of successful DNS exchanges.",
		),

		HTTPBodyStreamSeconds: newHistogram(
			"sonda_ptnop_http_body_stream_seconds",
			"Duration of successful HTTP body streams.",
		),

		HTTPRoundTripSeconds: newHistogram(
			"sonda_ptnop_http_round_trip_seconds",
			"Duration of successful HTTP round trips.",
		),

		TLSHandshakeSeconds: newHistogram(
			"sonda_ptnop_tls_handshake_seconds",
			"Duration of successful TLS handshakes.",
		),

		IOBytes: f.NewCounterVec(prometheus.CounterOpts{
			Name: "sonda_ptnop_io_bytes_total",
			Help: "Application-layer bytes read and written on ptnop conns.",
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
	metricsObserveStage(
		ms.ConnectSeconds, stage, uue.ConnectDuration, uue.ConnectErr)

	metricsObserveStage(
		ms.DNSExchangeSeconds, stage, uue.DNSExchangeDuration, uue.DNSExchangeErr)

	metricsObserveStage(
		ms.HTTPBodyStreamSeconds, stage, uue.HTTPBodyStreamDuration, uue.HTTPBodyStreamErr)

	metricsObserveStage(
		ms.HTTPRoundTripSeconds, stage, uue.HTTPRoundTripDuration, uue.HTTPRoundTripErr)

	metricsObserveStage(
		ms.TLSHandshakeSeconds, stage, uue.TLSHandshakeDuration, uue.TLSHandshakeErr)

	// 3. Count the bytes, skipping zero-bytes entries to avoid creating series for
	// spans that never exchanged data (e.g., a failed TCP connect).
	metricsAddIOBytes(ms.IOBytes, stage, "read", uue.ReadBytes)

	metricsAddIOBytes(ms.IOBytes, stage, "write", uue.WriteBytes)
}

func metricsAddIOBytes(cv *prometheus.CounterVec, stage prometheus.Labels, op string, count int64) {
	if count > 0 {
		labels := prometheus.Labels{"op": op}
		maps.Copy(labels, stage)
		cv.With(labels).Add(float64(count))
	}
}

// metricsObserveStage observes the duration of a stage if it succeeded.
//
// A stage that did not run has zero duration. A stage that failed or
// was skipped has a nonempty errClass. We also skip durations <= 0
// because t and t0 are wall-clock times: a clock step during the stage
// could make the difference zero or negative.
func metricsObserveStage(
	hv *prometheus.HistogramVec, labels prometheus.Labels, d time.Duration, errClass string) {
	if d > 0 && errClass == "" {
		hv.With(labels).Observe(d.Seconds())
	}
}
