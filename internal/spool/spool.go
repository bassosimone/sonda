// SPDX-License-Identifier: GPL-3.0-or-later

// Package spool implements the spool management API.
package spool

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// SLogger abstracts the [*slog.Logger] behavior for testing.
type SLogger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

// RootDir models the root directory of the spool.
//
// [RootDir.GarbageCollect] removes [SpanDir] older than the given cutoff time.
//
// [RootDir.NewDataTypeDir] is a lazy operation that only validates whether
// the given name is OK but does not otherwise modify the state (e.g. when the
// backing store is the file system, it does not create directories).
type RootDir interface {
	NewDataTypeDir(dataType string) (DataTypeDir, error)
	GarbageCollect(cutoff time.Time, logger SLogger)
}

// DataTypeDir models a data type directory inside the spool.
//
// Use [DataTypeDir.CreateTempSpanDir] to create a temporary span directory
// [TempSpanDir] in which to write named entities.
//
// Once a [TempSpanDir] has been published using [TempSpanDir.Publish], it becomes
// possible to open it as a [SpanDir] using [DataTypeDir.OpenSpanDir].
//
// Typical implementations will create persistent state (e.g. directories) on
// demand in [DataTypeDir.CreateTempSpanDir]. A permission denied error when
// using a file-system-based backend such as [NewRootDirFS] will thus only surface
// when executing this operation, since previous operations are lazy.
//
// Note that [DataTypeDir.OpenSpanDir] may be a lazy operation that does not
// check its backing store until you try to open resources.
type DataTypeDir interface {
	CreateTempSpanDir() (TempSpanDir, error)
	OpenSpanDir(spanID string) (SpanDir, error)
}

// TempSpanDir is a temporary span directory that allows you to obtain
// writable entities for saving measurement results.
//
// Use [TempSpanDir.CreateEntity] to obtain an [io.Writer] for
// writing into a named entity within the [TempSpanDir]. The resources
// associated with such an entity (typically open files) will be
// closed by [TempSpanDir.Publish] or [TempSpanDir.Close]: the first
// invoked method will close all files and prevent further writes.
//
// The [TempSpanDir.Publish] should be used on success and makes the
// [TempSpanDir] a proper [SpanDir] accessible by other callers using
// typically the [DataTypeDir.OpenSpanDir] method.
//
// The [TempSpanDir.Close] should be a deferred call to ensure we
// do not leak open resources on the error paths.
type TempSpanDir interface {
	Close() error
	ID() string
	CreateEntity(name string) (io.Writer, error)
	Publish() error
}

// SpanDir is a span directory containing named measurement entities.
//
// If [DataTypeDir.OpenSpanDir] is a lazy operation, errors caused by missing
// state surface in [SpanDir.OpenEntity] as [fs.ErrNotExist] errors that one
// may detect using the [errors.Is] functionality.
type SpanDir interface {
	Created() time.Time
	DataType() string
	ID() string
	OpenEntity(name string) (io.ReadCloser, error)
}

// spanUUID is the internal type modeling a specific spanID.
type spanUUID uuid.UUID

// newSpanUUID constructs and return a new [spanUUID].
func newSpanUUID() spanUUID {
	return spanUUID(uuid.Must(uuid.NewV7()))
}

// CreationTime returns the creation time of the given [spanUUID].
func (su spanUUID) CreationTime() time.Time {
	sec, nsec := uuid.UUID(su).Time().UnixTime()
	return time.Unix(sec, nsec)
}

// String returns the canonical string representation of the given [spanUUID].
func (su spanUUID) String() string {
	return uuid.UUID(su).String()
}

// ErrInvalidSpanID indicates that a span ID is invalid.
var ErrInvalidSpanID = errors.New("invalid spanID")

// parseSpanUUID parses a [spanUUID] from string.
func parseSpanUUID(value string) (spanUUID, error) {
	spanID, err := uuid.Parse(value)
	if err != nil {
		return spanUUID{}, fmt.Errorf("%w %q: %w", ErrInvalidSpanID, value, err)
	}
	if spanID.Version() != 7 {
		return spanUUID{}, fmt.Errorf("%w %q: invalid UUID version", ErrInvalidSpanID, value)
	}
	return spanUUID(spanID), nil
}

// NamePattern is the pattern that dataType and entity names must match.
const NamePattern = `^[a-zA-Z][a-zA-Z0-9_.-]*$`

// ErrInvalidName indicates that a name does not match [NamePattern].
var ErrInvalidName = errors.New("invalid name")

var nameRegexp = regexp.MustCompile(NamePattern)

func validateName(name string) error {
	if !nameRegexp.MatchString(name) {
		return fmt.Errorf("%w: expected %q, got %q", ErrInvalidName, NamePattern, name)
	}
	return nil
}

// CreateTempSpanDir is a convenience function to create a [TempSpanDir].
func CreateTempSpanDir(rootDir RootDir, dataType string) (TempSpanDir, error) {
	dataTypeDir, err := rootDir.NewDataTypeDir(dataType)
	if err != nil {
		return nil, err
	}
	return dataTypeDir.CreateTempSpanDir()
}

// OpenSpanDir is a convenience function to open a [SpanDir].
func OpenSpanDir(rootDir RootDir, dataType, spanID string) (SpanDir, error) {
	dataTypeDir, err := rootDir.NewDataTypeDir(dataType)
	if err != nil {
		return nil, err
	}
	return dataTypeDir.OpenSpanDir(spanID)
}
