// SPDX-License-Identifier: GPL-3.0-or-later

// Package triggersreader contains code to read triggers.
package triggersreader

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/spool"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/bassosimone/sonda/internal/triggers"
	"github.com/fsnotify/fsnotify"
	"github.com/google/uuid"
)

// drainMaxLineSize is the maximum accepted line size.
const drainMaxLineSize = 1 << 19

// Loop is the loop that processes the triggers dir in a loop.
//
// Construct using [NewLoop]; cleanup using [*Loop.Close].
//
// Fields must not be modified after invoking the [*Loop.Run] method.
type Loop struct {
	DataType string
	Env      *testable.Environ
	Logger   *slog.Logger
	MaxAge   time.Duration
	SpoolDir spool.RootDir
	runDir   string
	watcher  *fsnotify.Watcher
}

// NewLoop constructs a new [*Loop] with the given config.
//
// dataType is the expected data type (e.g. "ptnop").
//
// env is the testable environment.
//
// logger is used to emit log messages.
//
// maxAge is the maximum age a trigger or span should have in order
// to be considered, otherwise it would not be processed (though, the
// trigger file would be removed anyway).
//
// runDir is the directory containing triggers (e.g.
// `/run/sonda/etl-ptnop-archive`).
//
// spoolDir is the base spool directory (e.g. `/var/spool/sonda`).
func NewLoop(
	dataType string,
	env *testable.Environ,
	logger *slog.Logger,
	maxAge time.Duration,
	runDir string,
	spoolDir spool.RootDir,
) (*Loop, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := watcher.Add(runDir); err != nil {
		watcher.Close()
		return nil, err
	}
	loop := &Loop{
		DataType: dataType,
		Env:      env,
		Logger:   logger,
		MaxAge:   maxAge,
		runDir:   runDir,
		SpoolDir: spoolDir,
		watcher:  watcher,
	}
	return loop, nil
}

// Close stops the fsnotify watcher.
func (p *Loop) Close() error {
	return p.watcher.Close()
}

// ProcessorFunc processes a [spool.SpanDir] described by a trigger.
//
// Typically, it reads the span data to generate metrics.
//
// When this function returns an error, the error is logged but does
// not otherwise interrupt the triggers-watching loop.
type ProcessorFunc func(dir spool.SpanDir) error

// AfterWalkFunc performs maintenance actions after walking triggers (e.g.
// compressing old archived data or removing old archived data).
//
// When this function returns an error, the error is logged but does
// not otherwise interrupt the triggers-watching loop.
type AfterWalkFunc func(cutoff time.Time) error

// Run runs the loop that processes triggers.
//
// It waits for fsnotify events or periodic notifications and, once activated, it
// reads each trigger, removes it, and then calls `psf` to process spans.
//
// The `zzf` function is invoked after walking the triggers, and it is expected
// to perform maintenance tasks (e.g. removing old archived data).
func (p *Loop) Run(ctx context.Context, psf ProcessorFunc, zzf AfterWalkFunc) {
	runtimex.Assert(psf != nil && zzf != nil)
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		cutoff := time.Now().Add(-p.MaxAge)
		p.walkTriggers(cutoff, psf) // warnings already printed
		if err := zzf(cutoff); err != nil {
			p.Logger.Warn("AfterWalkFunc", slog.Any("err", err))
			// do not interrupt the loop here
		}
		if !p.waitEvents(ctx, ticker) {
			break
		}
	}
}

// waitEvents blocks until a context, ticker, or watcher event occurs.
//
// Returns false when the caller should stop waiting for events.
func (p *Loop) waitEvents(ctx context.Context, ticker *time.Ticker) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			return true
		case err, ok := <-p.watcher.Errors:
			runtimex.Assert(!ok || errors.Is(err, fsnotify.ErrEventOverflow))
			return ok
		case event, ok := <-p.watcher.Events:
			if ok && event.Has(fsnotify.Create) {
				return true
			}
		}
	}
}

// walkTriggers does a pass over the trigger directory inside the triggers dir.
func (p *Loop) walkTriggers(cutoff time.Time, fx ProcessorFunc) error {
	p.Logger.Info("processing triggers dir", slog.String("dir", p.runDir))

	dentries, err := p.Env.ReadDir(p.runDir)
	if err != nil {
		p.Logger.Error("env.ReadDir", slog.Any("err", err))
		return err
	}

	var errv []error
	for _, dentry := range dentries {
		err := p.processDentry(cutoff, dentry, fx)
		if err != nil {
			p.Logger.Warn("processDentry", slog.Any("err", err))
			errv = append(errv, err)
			continue
		}
	}

	return errors.Join(errv...)
}

// processDentry drains a single dentry from the triggers dir.
func (p *Loop) processDentry(cutoff time.Time, dentry fs.DirEntry, fx ProcessorFunc) error {
	// 1. The dentry must be a regular `<UUIDv7>.jsonl` file.
	name, mode := dentry.Name(), dentry.Type()
	fullFilePath := filepath.Join(p.runDir, name)
	if !mode.IsRegular() {
		return fmt.Errorf("not a regular file: %s", fullFilePath)
	}
	spanID, err := uuid.Parse(strings.TrimSuffix(name, ".jsonl"))
	if err != nil {
		return fmt.Errorf("not a UUID file name: %s: %w", fullFilePath, err)
	}
	if spanID.Version() != 7 {
		return fmt.Errorf("invalid UUID version: %s", fullFilePath)
	}

	// 2. From now on, we remove the file on exit/panic.
	defer p.Env.Remove(fullFilePath)

	// 3. The file must have been created after the cutoff.
	sec, nsec := spanID.Time().UnixTime()
	ts := time.Unix(sec, nsec)
	if ts.Before(cutoff) {
		p.Logger.Warn("skip old dentry", slog.String("dentry", fullFilePath))
		return nil
	}

	// 4. Play it safe and read the file line by line. We assume the file
	// is reasonably small, but it is best to write robust code here.
	filep, err := p.Env.OpenFile(fullFilePath, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("env.OpenFile: %w", err)
	}
	defer filep.Close()
	scanner := bufio.NewScanner(filep)
	scanner.Buffer(nil, drainMaxLineSize)

	var errv []error
	for scanner.Scan() {
		// 4.1. Parse information about a created span and fail hard if
		// the parsing fails: it means a corrupt trigger file.
		var info triggers.CreatedSpan
		if err := json.Unmarshal(scanner.Bytes(), &info); err != nil {
			return fmt.Errorf("%s: %w", fullFilePath, err)
		}

		// 4.2. Otherwise process each span described by the trigger.
		err := p.processSpan(cutoff, fx, &info)
		if err != nil {
			p.Logger.Warn("cannot process span", slog.Any("err", err))
			errv = append(errv, err)
			continue
		}
	}

	// 5. Handle errors that may have occurred.
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner.Err: %w", err)
	}
	if len(errv) >= 1 {
		return errors.New("failed to process at least one span")
	}
	return nil
}

// processSpan processes a single created span.
func (p *Loop) processSpan(cutoff time.Time, fx ProcessorFunc, info *triggers.CreatedSpan) error {
	// 1. The span data type must be the desired data type.
	if info.DataType != p.DataType {
		p.Logger.Info("skip unhandled data type", slog.String("dataType", info.DataType))
		return nil
	}

	// 2. Make sure the spanID is valid and created after the cutoff.
	spanDir, err := spool.OpenSpanDir(p.SpoolDir, p.DataType, info.SpanID)
	if err != nil {
		return fmt.Errorf("spool.OpenSpanDir: %w", err)
	}
	if spanDir.Created().Before(cutoff) {
		p.Logger.Info("skip old span", slog.String("spanID", info.SpanID))
		return nil
	}

	// 3. Delegate to the user-specific [ProcessorFunc].
	return fx(spanDir)
}
