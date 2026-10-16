// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/bassosimone/sonda/internal/testable"
)

// expireOldDays removes the daily files, compressed or not, whose day
// ended before or at cutoff.
//
// Returns the joined errors of the files we could not remove.
func expireOldDays(
	env *testable.Environ,
	logger *slog.Logger,
	ptnopArchiveDir string,
	cutoff time.Time,
) error {
	dentries, err := env.ReadDir(ptnopArchiveDir)
	if err != nil {
		logger.Warn("env.ReadDir", slog.Any("err", err))
		return err
	}

	var errv []error
	for _, dentry := range dentries {
		// 1. only consider the daily files.
		name := dentry.Name()
		base := strings.TrimSuffix(name, ".zst")
		if !dentry.Type().IsRegular() || !strings.HasSuffix(base, compressDailySuffix) {
			continue
		}
		day, err := time.Parse("20060102", strings.TrimSuffix(base, compressDailySuffix))
		if err != nil {
			logger.Warn("time.Parse", slog.Any("err", err), slog.String("name", name))
			continue
		}

		// 2. skip the days that are still within the retention period.
		if day.AddDate(0, 0, 1).After(cutoff) {
			continue
		}

		// 3. remove the file.
		path := filepath.Join(ptnopArchiveDir, name)
		if err := env.Remove(path); err != nil {
			logger.Warn("env.Remove", slog.Any("err", err), slog.String("path", path))
			errv = append(errv, err)
			continue
		}
		logger.Info("expireOldDays", slog.String("path", path))
	}
	return errors.Join(errv...)
}
