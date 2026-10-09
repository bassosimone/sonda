// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bassosimone/sonda/internal/testable"
	"github.com/prometheus/client_golang/prometheus"
)

// cgroupDefaultSliceDir is the cgroup v2 directory of `system-sonda.slice`.
const cgroupDefaultSliceDir = "/sys/fs/cgroup/system.slice/system-sonda.slice"

// cgroupCollector exports the resource usage of the sonda slice.
//
// It reads the cgroup v2 interface files of the slice at each scrape, so it
// keeps no state. The slice counters include the usage of children that have
// already exited (e.g., `sonda-scan.service`), which is why we read the slice
// rather than the individual units.
//
// When a file cannot be read or parsed, we log and skip the corresponding
// metrics rather than failing the scrape to avoid returning a 500.
//
// See https://docs.kernel.org/admin-guide/cgroup-v2.html for the format
// and the semantics of the interface files.
type cgroupCollector struct {
	cpuSeconds      *prometheus.Desc
	env             *testable.Environ
	ioBytes         *prometheus.Desc
	logger          *slog.Logger
	memoryBytes     *prometheus.Desc
	memoryPeakBytes *prometheus.Desc
	sliceDir        string
	tasks           *prometheus.Desc
}

var _ prometheus.Collector = &cgroupCollector{}

// newCgroupCollector creates a [*cgroupCollector] reading from sliceDir.
func newCgroupCollector(
	env *testable.Environ, logger *slog.Logger, sliceDir string) *cgroupCollector {

	return &cgroupCollector{
		cpuSeconds: prometheus.NewDesc(
			"sonda_slice_cpu_seconds_total",
			"CPU time consumed by the sonda slice (cpu.stat).",
			[]string{"mode"}, nil,
		),

		env: env,

		ioBytes: prometheus.NewDesc(
			"sonda_slice_io_bytes_total",
			"Block I/O bytes of the sonda slice per device (io.stat).",
			[]string{"device", "op"}, nil,
		),

		logger: logger,

		memoryBytes: prometheus.NewDesc(
			"sonda_slice_memory_bytes",
			"Memory used by the sonda slice (memory.stat).",
			[]string{"kind"}, nil,
		),

		memoryPeakBytes: prometheus.NewDesc(
			"sonda_slice_memory_peak_bytes",
			"Peak memory usage of the sonda slice (memory.peak).",
			nil, nil,
		),

		sliceDir: sliceDir,

		tasks: prometheus.NewDesc(
			"sonda_slice_tasks",
			"Number of tasks in the sonda slice (pids.current).",
			nil, nil,
		),
	}
}

// Describe implements [prometheus.Collector].
func (cc *cgroupCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- cc.cpuSeconds
	ch <- cc.ioBytes
	ch <- cc.memoryBytes
	ch <- cc.memoryPeakBytes
	ch <- cc.tasks
}

// Collect implements [prometheus.Collector].
func (cc *cgroupCollector) Collect(ch chan<- prometheus.Metric) {
	// 1. CPU time. The kernel reports microseconds.
	if kv, err := cc.readFlatKeyed("cpu.stat"); err == nil {
		for _, mode := range []string{"system", "user"} {
			if v, ok := kv[mode+"_usec"]; ok {
				ch <- prometheus.MustNewConstMetric(
					cc.cpuSeconds, prometheus.CounterValue, float64(v)/1e6, mode)
			}
		}
	}

	// 2. Block I/O per device. We do not sum across devices: stacked devices (e.g.,
	// dm-crypt over NVMe) count the same bytes once per layer.
	if devs, err := cc.readNestedKeyed("io.stat"); err == nil {
		for dev, kv := range devs {
			for op, key := range map[string]string{"read": "rbytes", "write": "wbytes"} {
				if v, ok := kv[key]; ok {
					ch <- prometheus.MustNewConstMetric(
						cc.ioBytes, prometheus.CounterValue, float64(v), dev, op)
				}
			}
		}
	}

	// 3. Memory. We split anonymous memory from the page cache because the
	// latter grows with the files we write and the kernel can reclaim it.
	if kv, err := cc.readFlatKeyed("memory.stat"); err == nil {
		for _, kind := range []string{"anon", "file"} {
			if v, ok := kv[kind]; ok {
				ch <- prometheus.MustNewConstMetric(
					cc.memoryBytes, prometheus.GaugeValue, float64(v), kind)
			}
		}
	}

	if v, err := cc.readSingleValue("memory.peak"); err == nil {
		ch <- prometheus.MustNewConstMetric(cc.memoryPeakBytes, prometheus.GaugeValue, float64(v))
	}

	// 4. Tasks (i.e., threads, not processes).
	if v, err := cc.readSingleValue("pids.current"); err == nil {
		ch <- prometheus.MustNewConstMetric(cc.tasks, prometheus.GaugeValue, float64(v))
	}
}

// readFile reads an interface file and logs on failure.
func (cc *cgroupCollector) readFile(name string) (string, error) {
	data, err := cc.env.ReadFile(filepath.Join(cc.sliceDir, name))
	if err != nil {
		cc.logger.Warn("cgroup: env.ReadFile", slog.String("file", name), slog.Any("err", err))
		return "", err
	}
	return string(data), nil
}

// readSingleValue reads a "single value" file (e.g., `pids.current`).
func (cc *cgroupCollector) readSingleValue(name string) (uint64, error) {
	data, err := cc.readFile(name)
	if err != nil {
		return 0, err
	}
	val, err := strconv.ParseUint(strings.TrimSpace(data), 10, 64)
	if err != nil {
		cc.logger.Warn("cgroup: strconv.ParseUint", slog.String("file", name), slog.Any("err", err))
		return 0, err
	}
	return val, nil
}

// readFlatKeyed reads a "flat keyed" file with `KEY VALUE` lines (e.g., `cpu.stat`).
//
// Lines whose value is not an unsigned integer are skipped.
func (cc *cgroupCollector) readFlatKeyed(name string) (map[string]uint64, error) {
	data, err := cc.readFile(name)
	if err != nil {
		return nil, err
	}
	out := make(map[string]uint64)
	for line := range strings.SplitSeq(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if v, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
			out[fields[0]] = v
		}
	}
	return out, nil
}

// readNestedKeyed reads a "nested keyed" file with `KEY SUBKEY=VALUE...`
// lines (e.g., `io.stat`) and returns a map from KEY to SUBKEY to VALUE.
//
// Pairs whose value is not an unsigned integer are skipped.
func (cc *cgroupCollector) readNestedKeyed(name string) (map[string]map[string]uint64, error) {
	data, err := cc.readFile(name)
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]uint64)
	for line := range strings.SplitSeq(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) <= 0 {
			continue
		}
		kv := make(map[string]uint64)
		for _, field := range fields[1:] {
			key, value, found := strings.Cut(field, "=")
			if !found {
				continue
			}
			if v, err := strconv.ParseUint(value, 10, 64); err == nil {
				kv[key] = v
			}
		}
		out[fields[0]] = kv
	}
	return out, nil
}
