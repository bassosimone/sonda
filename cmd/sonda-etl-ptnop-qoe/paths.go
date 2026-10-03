// SPDX-License-Identifier: GPL-3.0-or-later

package main

import "path/filepath"

// The files this pipeline adds to a ptnop span directory are named after
// its destination data type (`qoe`). Other pipelines reading the same span
// use their own destination name, so their files do not collide.

// spanMetricsParquet returns the path to qoe.parquet inside a span directory.
func spanMetricsParquet(spanDir string) string {
	return filepath.Join(spanDir, "qoe.parquet")
}

// spanMetricsParquetTmp returns the temporary path for qoe.parquet
// (before atomic rename).
func spanMetricsParquetTmp(spanDir string) string {
	return filepath.Join(spanDir, "qoe.parquet.tmp")
}

// spanMetricsLoaded returns the path to the sentinel file that marks
// a span's metrics as loaded into the daily aggregate.
func spanMetricsLoaded(spanDir string) string {
	return filepath.Join(spanDir, "qoe.loaded")
}
