// SPDX-License-Identifier: GPL-3.0-or-later

package vspool

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/bassosimone/closepool"
	"github.com/bassosimone/sonda/internal/ptnoppaths"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/google/uuid"
)

// SpoolDir is the filesystem-backed spool-dir implementation.
//
// Construct using [NewSpoolDir].
type SpoolDir struct {
	env  *testable.Environ
	path string
}

// NewSpoolDir constructs and returns new [*SpoolDir] using the given `spoolDir`.
func NewSpoolDir(env *testable.Environ, spoolDir string) (*SpoolDir, error) {
	spoolDir, err := env.Abs(spoolDir)
	if err != nil {
		return nil, fmt.Errorf("env.Abs: %w", err)
	}
	dd := &SpoolDir{env: env, path: spoolDir}
	return dd, nil
}

// ErrInvalidDataType indicates that a datatype name is invalid.
var ErrInvalidDataType = errors.New("invalid datatype")

// validDataType contains the valid datatype names.
var validDataType = map[string]bool{
	"ptnop": true,
}

// Note that `/var/spool/sonda` is `02750 _sonda:_sonda` so we use `0750` when creating
// directories and `0640` when creating files to allow `_sonda` to read.
const (
	dirPerms  = 0750
	filePerms = 0640
)

// NewSpanConfig creates a new [*SpanConfig] for the given `dataType`.
func (dd *SpoolDir) NewSpanConfig(dataType string) (*SpanConfig, error) {
	if !validDataType[dataType] {
		return nil, fmt.Errorf("CreateSpanDir: %w: %s", ErrInvalidDataType, dataType)
	}
	spanID := uuid.Must(uuid.NewV7()).String()
	spanDir := ptnoppaths.SpanDir(filepath.Join(dd.path, dataType), spanID)
	tmpDir := ptnoppaths.SpanDirTmp(filepath.Join(dd.path, dataType), spanID)
	sc := &SpanConfig{
		BodyFile:   false,
		StdoutFile: false,
		spanID:     spanID,
		spanDir:    spanDir,
		tmpDir:     tmpDir,
	}
	return sc, nil
}

// SpanConfig contains the uncommitted span configuration.
type SpanConfig struct {
	// BodyFile controls whether we need to create a body file.
	BodyFile bool

	// StdoutFile controls whether we need to create a stdout file.
	StdoutFile bool

	spanID  string
	spanDir string
	tmpDir  string
}

// SpanID returns the minted span ID.
func (sc *SpanConfig) SpanID() string {
	return sc.spanID
}

// openFlags contains the common flags used to open span dir files.
const openFlags = os.O_CREATE | os.O_TRUNC | os.O_WRONLY

// NewSpanDir instantiates a [*SpanDir] from the given [*SpanConfig].
func (dd *SpoolDir) NewSpanDir(cfg *SpanConfig) (*SpanDir, error) {
	// 1. Initialize the span dir struct.
	sd := &SpanDir{
		bodyWriter:   io.Discard,
		closers:      &closepool.Pool{},
		env:          dd.env,
		once:         sync.Once{},
		spanDir:      cfg.spanDir,
		spanID:       cfg.spanID,
		stdoutWriter: io.Discard,
		tmpDir:       cfg.tmpDir,
	}

	// 2. create the temporary directory.
	if err := sd.env.MkdirAll(sd.tmpDir, dirPerms); err != nil {
		return nil, fmt.Errorf("NewSpanDir: %w", err)
	}

	// 3. Maybe open the file where to write the body.
	if cfg.BodyFile {
		path := ptnoppaths.SpanBodyBin(sd.tmpDir)
		filep, err := sd.env.OpenFile(path, openFlags, filePerms)
		if err != nil {
			sd.closers.Close()
			return nil, fmt.Errorf("NewSpanDir: %w", err)
		}
		sd.closers.Add(filep)
		sd.bodyWriter = filep
	}

	// 4. Maybe open the file where to write the stdout.
	if cfg.StdoutFile {
		path := ptnoppaths.SpanStdout(sd.tmpDir)
		filep, err := sd.env.OpenFile(path, openFlags, filePerms)
		if err != nil {
			sd.closers.Close()
			return nil, fmt.Errorf("NewSpanDir: %w", err)
		}
		sd.closers.Add(filep)
		sd.stdoutWriter = filep
	}

	return sd, nil
}

// SpanDir is the directory containing span data.
//
// Returned by [*SpoolDir.CreateSpanDir].
//
// Close using either [*SpanDir.Rollback] or [*SpanDir.Commit].
type SpanDir struct {
	bodyWriter   io.Writer
	closers      *closepool.Pool
	env          *testable.Environ
	once         sync.Once // to make close idempotent
	spanDir      string
	spanID       string
	stdoutWriter io.Writer
	tmpDir       string
}

// BodyWriter returns the [io.Writer] for writing the body.
//
// We return [io.Discard] if no body writer has been requested with [*SpanConfig].
func (sd *SpanDir) BodyWriter() io.Writer {
	return sd.bodyWriter
}

// StdoutWriter returns the [io.Writer] for writing the stdout.
//
// We return [io.Discard] if no body writer has been requested with [*SpanConfig].
func (sd *SpanDir) StdoutWriter() io.Writer {
	return sd.stdoutWriter
}

// Path returns the span directory path.
func (sd *SpanDir) Path() string {
	return sd.spanDir
}

// Rollback closes the open files but keeps around the temporary directory for
// inspection until the `sonda-spool gc` command eventually GCs it.
//
// This method has no effect if [*SpanDir.Commit] has already been invoked.
func (sd *SpanDir) Rollback() (err error) {
	sd.once.Do(func() {
		err = sd.closers.Close()
	})
	return
}

// Commit closes the open files and renames the temporary dir.
//
// This method has no effect if [*SpanDir.Rollback] has already been invoked.
func (sd *SpanDir) Commit() (err error) {
	sd.once.Do(func() {
		err = sd.commit()
	})
	return
}

func (sd *SpanDir) commit() error {
	if err := sd.closers.Close(); err != nil {
		return err
	}
	return sd.env.Rename(sd.tmpDir, sd.spanDir)
}
