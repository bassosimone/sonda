// SPDX-License-Identifier: GPL-3.0-or-later

package spool

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bassosimone/closepool"
	"github.com/bassosimone/sonda/internal/testable"
)

// fsDirMode is the file mode for directories.
const fsDirMode = 0750

// fsFileMode is the file mode for files.
const fsFileMode = 0640

// fsJoinSpanDirPath returns the [SpanDir] path.
func fsJoinSpanDirPath(parent, spanID string) string {
	return filepath.Join(parent, spanID[:4], spanID[4:5], spanID[5:6], spanID)
}

// fsJoinTempSpanDirPath returns the [TempSpanDir] path.
func fsJoinTempSpanDirPath(parent, spanID string) string {
	return fsJoinSpanDirPath(parent, spanID) + ".tmp"
}

// NewRootDirFS constructs a new [RootDir] using the file system as a backing store.
//
// This operation is lazy: directories are created on demand. The only possible failure
// mode is that we fail to transform the given path into an absolute path.
func NewRootDirFS(env *testable.Environ, path string) (RootDir, error) {
	path, err := env.Abs(path)
	if err != nil {
		return nil, err
	}
	root := &fsRootDir{Env: env, Path: path}
	return root, nil
}

// fsRootDir models the root directory of the spool.
type fsRootDir struct {
	Env  *testable.Environ
	Path string
}

var _ RootDir = &fsRootDir{}

// GarbageCollect implements [RootDir].
func (d *fsRootDir) GarbageCollect(cutoff time.Time, logger SLogger) {
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

func (d *fsRootDir) gcWalkShard(cutoff time.Time, logger SLogger, path string, z int) {
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

func (d *fsRootDir) gcMaybeRemoveSpan(cutoff time.Time, logger SLogger, name, parent string) {
	// Make sure we gc ".tmp" entries as well by removing the optional `.tmp` suffix.
	candidateSpanID := strings.TrimSuffix(name, ".tmp")
	spanID, err := parseSpanUUID(candidateSpanID)
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

// NewDataTypeDir implements [RootDir].
func (d *fsRootDir) NewDataTypeDir(dataType string) (DataTypeDir, error) {
	if err := validateName(dataType); err != nil {
		return nil, err
	}
	dpath := filepath.Join(d.Path, dataType)
	return &fsDataTypeDir{d.Env, dataType, dpath}, nil
}

type fsDataTypeDir struct {
	env  *testable.Environ
	name string
	path string
}

var _ DataTypeDir = &fsDataTypeDir{}

// CreateTempSpanDir implements [DataTypeDir].
func (d *fsDataTypeDir) CreateTempSpanDir() (TempSpanDir, error) {
	spanID := newSpanUUID().String()
	spanDir := fsJoinSpanDirPath(d.path, spanID)
	tmpDir := fsJoinTempSpanDirPath(d.path, spanID)

	// Note: this also creates the root, data type, and shard directories if needed.
	if err := d.env.MkdirAll(tmpDir, fsDirMode); err != nil {
		return nil, err
	}
	tsd := &fsTempSpanDir{
		closers: closepool.Pool{},
		env:     d.env,
		spanDir: spanDir,
		spanID:  spanID,
		tmpDir:  tmpDir,
	}
	return tsd, nil
}

type fsTempSpanDir struct {
	closers closepool.Pool
	env     *testable.Environ
	spanDir string
	spanID  string
	tmpDir  string
}

var _ TempSpanDir = &fsTempSpanDir{}

// Close implements [TempSpanDir].
func (d *fsTempSpanDir) Close() error {
	return d.closers.Close()
}

// CreateEntity implements [TempSpanDir].
func (d *fsTempSpanDir) CreateEntity(name string) (io.Writer, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	openFlags := os.O_CREATE | os.O_EXCL | os.O_WRONLY
	filep, err := d.env.OpenFile(filepath.Join(d.tmpDir, name+".gz"), openFlags, fsFileMode)
	if err != nil {
		return nil, err
	}
	d.closers.Add(filep)
	gzWriteCloser := newGzipLockedWriteCloser(filep)
	d.closers.Add(gzWriteCloser)
	return gzWriteCloser, nil
}

// ID implements [TempSpanDir].
func (d *fsTempSpanDir) ID() string {
	return d.spanID
}

// Publish implements [TempSpanDir].
func (d *fsTempSpanDir) Publish() error {
	if err := d.Close(); err != nil {
		return err
	}
	return d.env.Rename(d.tmpDir, d.spanDir)
}

// OpenSpanDir implements [DataTypeDir].
func (d *fsDataTypeDir) OpenSpanDir(spanID string) (SpanDir, error) {
	parsed, err := parseSpanUUID(spanID)
	if err != nil {
		return nil, err
	}
	sd := &fsSpanDir{
		env:      d.env,
		dataType: d.name,
		spanDir:  fsJoinSpanDirPath(d.path, parsed.String()), // make sure we normalize the UUID
		spanID:   parsed,
	}
	return sd, nil
}

type fsSpanDir struct {
	env      *testable.Environ
	dataType string
	spanID   spanUUID
	spanDir  string
}

var _ SpanDir = &fsSpanDir{}

// Created implements [SpanDir].
func (d *fsSpanDir) Created() time.Time {
	return d.spanID.CreationTime()
}

// DataType implements [SpanDir].
func (d *fsSpanDir) DataType() string {
	return d.dataType
}

// ID implements [SpanDir].
func (d *fsSpanDir) ID() string {
	return d.spanID.String()
}

// OpenEntity implements [SpanDir].
func (d *fsSpanDir) OpenEntity(name string) (io.ReadCloser, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	return openGzipReadCloser(d.env, filepath.Join(d.spanDir, name+".gz"))
}
