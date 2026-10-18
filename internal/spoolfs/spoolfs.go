// SPDX-License-Identifier: GPL-3.0-or-later

// Package spoolfs implements [spool] using the filesystem as the backing store.
package spoolfs

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bassosimone/closepool"
	"github.com/bassosimone/sonda/internal/gzipx"
	"github.com/bassosimone/sonda/internal/spool"
	"github.com/bassosimone/sonda/internal/testable"
)

// dirMode is the file mode for directories.
const dirMode = 0750

// fileMode is the file mode for files.
const fileMode = 0640

// joinSpanDirPath returns the [spool.SpanDir] path.
func joinSpanDirPath(parent, spanID string) string {
	return filepath.Join(parent, spanID[:4], spanID[4:5], spanID[5:6], spanID)
}

// joinTempSpanDirPath returns the [spool.TempSpanDir] path.
func joinTempSpanDirPath(parent, spanID string) string {
	return joinSpanDirPath(parent, spanID) + ".tmp"
}

// NewRootDir constructs a new [spool.RootDir] using the file system as a backing store.
//
// This operation is lazy: directories are created on demand. The only possible failure
// mode is that we fail to transform the given path into an absolute path.
func NewRootDir(env *testable.Environ, path string) (spool.RootDir, error) {
	path, err := env.Abs(path)
	if err != nil {
		return nil, err
	}
	root := &rootDir{Env: env, Path: path}
	return root, nil
}

// rootDir models the root directory of the spool.
type rootDir struct {
	Env  *testable.Environ
	Path string
}

var _ spool.RootDir = &rootDir{}

// GarbageCollect implements [spool.RootDir].
func (d *rootDir) GarbageCollect(cutoff time.Time, logger spool.SLogger) {
	entries, err := d.Env.ReadDir(d.Path)
	if err != nil {
		logger.Warn(
			"env.ReadDir",
			slog.Any("err", err),
			slog.String("path", d.Path),
		)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			logger.Warn(
				"unexpectedDirEntry",
				slog.String("entryName", entry.Name()),
				slog.Uint64("entryType", uint64(entry.Type())),
				slog.String("path", d.Path),
			)
			continue // not created by us
		}

		d.gcWalkShard(cutoff, logger, filepath.Join(d.Path, entry.Name()), 3)
	}
}

func (d *rootDir) gcWalkShard(cutoff time.Time, logger spool.SLogger, path string, z int) {
	entries, err := d.Env.ReadDir(path)
	if err != nil {
		logger.Warn(
			"env.ReadDir",
			slog.Any("err", err),
			slog.String("path", path),
		)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			logger.Warn(
				"unexpectedDirEntry",
				slog.String("entryName", entry.Name()),
				slog.Uint64("entryType", uint64(entry.Type())),
				slog.String("path", path),
			)
			continue // not created by us
		}

		if z > 0 {
			child := filepath.Join(path, entry.Name())
			d.gcWalkShard(cutoff, logger, child, z-1)
			_ = d.Env.Remove(child) // succeeds only if the directory is empty
		} else {
			d.gcMaybeRemoveSpan(cutoff, logger, entry.Name(), path)
		}
	}
}

func (d *rootDir) gcMaybeRemoveSpan(cutoff time.Time, logger spool.SLogger, name, parent string) {
	// Make sure we gc ".tmp" entries as well by removing the optional `.tmp` suffix.
	candidateSpanID := strings.TrimSuffix(name, ".tmp")
	spanID, err := spool.ParseSpanUUID(candidateSpanID)
	if err != nil {
		logger.Warn(
			"parseSpanID",
			slog.String("candidateSpanID", candidateSpanID),
			slog.Any("err", err),
			slog.String("name", name),
			slog.String("path", parent),
		)
		return
	}

	// Determine whether this span directory is too new to remove.
	creationTime := spanID.CreationTime()
	if !creationTime.Before(cutoff) {
		logger.Debug(
			"spanDirTooNew",
			slog.Time("creationTime", creationTime),
			slog.Time("cutoff", cutoff),
			slog.String("name", name),
			slog.String("path", parent),
		)
		return
	}

	// Recursively remove the span directory entry.
	spanPath := filepath.Join(parent, name)
	if err := d.Env.RemoveAll(spanPath); err != nil {
		logger.Warn(
			"env.RemoveAll",
			slog.Any("err", err),
			slog.String("path", spanPath),
		)
		return
	}
	logger.Info(
		"removedSpanDir",
		slog.String("path", spanPath),
	)
}

// NewDataTypeDir implements [spool.RootDir].
func (d *rootDir) NewDataTypeDir(dataType string) (spool.DataTypeDir, error) {
	if err := spool.ValidateName(dataType); err != nil {
		return nil, err
	}
	dpath := filepath.Join(d.Path, dataType)
	return &dataTypeDir{d.Env, dataType, dpath}, nil
}

type dataTypeDir struct {
	env  *testable.Environ
	name string
	path string
}

var _ spool.DataTypeDir = &dataTypeDir{}

// CreateTempSpanDir implements [spool.DataTypeDir].
func (d *dataTypeDir) CreateTempSpanDir() (spool.TempSpanDir, error) {
	spanID := spool.NewSpanUUID().String()
	publishPath := joinSpanDirPath(d.path, spanID)
	tmpPath := joinTempSpanDirPath(d.path, spanID)

	// Note: this also creates the root, data type, and shard directories if needed.
	if err := d.env.MkdirAll(tmpPath, dirMode); err != nil {
		return nil, err
	}
	tsd := &tempSpanDir{
		closers:     closepool.Pool{},
		env:         d.env,
		publishPath: publishPath,
		spanID:      spanID,
		tmpPath:     tmpPath,
	}
	return tsd, nil
}

type tempSpanDir struct {
	closers     closepool.Pool
	env         *testable.Environ
	publishPath string
	spanID      string
	tmpPath     string
}

var _ spool.TempSpanDir = &tempSpanDir{}

// Close implements [spool.TempSpanDir].
func (d *tempSpanDir) Close() error {
	return d.closers.Close()
}

// CreateEntity implements [spool.TempSpanDir].
func (d *tempSpanDir) CreateEntity(name string) (io.Writer, error) {
	if err := spool.ValidateName(name); err != nil {
		return nil, err
	}
	openFlags := os.O_CREATE | os.O_EXCL | os.O_WRONLY
	filep, err := d.env.OpenFile(filepath.Join(d.tmpPath, name+".gz"), openFlags, fileMode)
	if err != nil {
		return nil, err
	}
	d.closers.Add(filep)
	gzWriteCloser := gzipx.NewLockedWriteCloser(filep)
	d.closers.Add(gzWriteCloser)
	return gzWriteCloser, nil
}

// ID implements [spool.TempSpanDir].
func (d *tempSpanDir) ID() string {
	return d.spanID
}

// Publish implements [spool.TempSpanDir].
func (d *tempSpanDir) Publish() error {
	if err := d.Close(); err != nil {
		return err
	}
	return d.env.Rename(d.tmpPath, d.publishPath)
}

// OpenSpanDir implements [spool.DataTypeDir].
func (d *dataTypeDir) OpenSpanDir(spanID string) (spool.SpanDir, error) {
	parsed, err := spool.ParseSpanUUID(spanID)
	if err != nil {
		return nil, err
	}
	sd := &spanDir{
		env:      d.env,
		dataType: d.name,
		path:     joinSpanDirPath(d.path, parsed.String()), // make sure we normalize the UUID
		spanID:   parsed,
	}
	return sd, nil
}

type spanDir struct {
	env      *testable.Environ
	dataType string
	spanID   spool.SpanUUID
	path     string
}

var _ spool.SpanDir = &spanDir{}

// Created implements [spool.SpanDir].
func (d *spanDir) Created() time.Time {
	return d.spanID.CreationTime()
}

// DataType implements [spool.SpanDir].
func (d *spanDir) DataType() string {
	return d.dataType
}

// ID implements [spool.SpanDir].
func (d *spanDir) ID() string {
	return d.spanID.String()
}

// OpenEntity implements [spool.SpanDir].
func (d *spanDir) OpenEntity(name string) (io.ReadCloser, error) {
	if err := spool.ValidateName(name); err != nil {
		return nil, err
	}
	return gzipx.OpenReadCloser(d.env, filepath.Join(d.path, name+".gz"))
}
