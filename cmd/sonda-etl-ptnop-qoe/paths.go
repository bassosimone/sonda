// SPDX-License-Identifier: GPL-3.0-or-later

package main

import "path/filepath"

// spanMetricsParquet returns the path to metrics.parquet inside a span directory.
func spanMetricsParquet(spanDir string) string {
	return filepath.Join(spanDir, "metrics.parquet")
}

// spanMetricsParquetTmp returns the temporary path for metrics.parquet
// (before atomic rename).
func spanMetricsParquetTmp(spanDir string) string {
	return filepath.Join(spanDir, "metrics.parquet.tmp")
}

// spanMetricsLoaded returns the path to the sentinel file that marks
// a span's metrics as loaded into the daily aggregate.
func spanMetricsLoaded(spanDir string) string {
	return filepath.Join(spanDir, "metrics.loaded")
}
