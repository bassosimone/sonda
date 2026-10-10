// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"github.com/prometheus/client_golang/prometheus"
)

// metricsSet contains the Prometheus metrics we export.
type metricsSet struct{}

// newMetricsSet creates a [*metricsSet] registered into reg.
func newMetricsSet(reg prometheus.Registerer) *metricsSet {
	return &metricsSet{}
}

// Observe updates the metrics using the given unified event.
func (ms *metricsSet) Observe(uue *updateUnifiedEvent) {
	// TODO(bassosimone): implement
}
