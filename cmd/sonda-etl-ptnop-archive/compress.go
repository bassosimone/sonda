// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bassosimone/sonda/internal/testable"
	"github.com/klauspost/compress/zstd"
)

// compressDailySuffix is the suffix of the uncompressed daily files.
const compressDailySuffix = "T000000Z.jsonl"

// compressClosedDays compresses the daily files no run can append to anymore.
//
// A day is closed when its end is not after cutoff: since we skip spans older
// than cutoff, no span can belong to such a day anymore.
//
// Returns the joined errors of the files we could not compress.
func compressClosedDays(
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
		// 1. only consider the uncompressed daily files.
		name := dentry.Name()
		if !dentry.Type().IsRegular() || !strings.HasSuffix(name, compressDailySuffix) {
			continue
		}
		day, err := time.Parse("20060102", strings.TrimSuffix(name, compressDailySuffix))
		if err != nil {
			logger.Warn("time.Parse", slog.Any("err", err), slog.String("name", name))
			continue
		}

		// 2. skip the days that are not closed yet.
		if day.AddDate(0, 0, 1).After(cutoff) {
			continue
		}

		// 3. compress the file.
		srcPath := filepath.Join(ptnopArchiveDir, name)
		if err := compressFile(env, srcPath, srcPath+".zst"); err != nil {
			logger.Warn("compressFile", slog.Any("err", err), slog.String("path", srcPath))
			errv = append(errv, err)
			continue
		}
		logger.Info("compressFile", slog.String("path", srcPath))
	}
	return errors.Join(errv...)
}

// compressFile compresses srcPath into dstPath and removes srcPath.
func compressFile(env *testable.Environ, srcPath, dstPath string) error {
	tmpPath := dstPath + ".tmp"
	if err := compressFileTmp(env, srcPath, tmpPath); err != nil {
		env.Remove(tmpPath)
		return err
	}
	if err := env.Rename(tmpPath, dstPath); err != nil {
		env.Remove(tmpPath)
		return err
	}
	return env.Remove(srcPath)
}

// compressFileTmp compresses srcPath into tmpPath and fsyncs tmpPath.
func compressFileTmp(env *testable.Environ, srcPath, tmpPath string) error {
	// 1. open the source and the destination.
	source, err := env.OpenFile(srcPath, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer source.Close()

	dest, err := env.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0640)
	if err != nil {
		return err
	}
	defer dest.Close()

	// 2. compress, flushing the encoder with Close.
	enc, err := zstd.NewWriter(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(enc, source); err != nil {
		enc.Close()
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}

	// 3. make sure the data reaches the disk before the rename.
	if err := dest.Sync(); err != nil {
		return err
	}
	return dest.Close()
}
