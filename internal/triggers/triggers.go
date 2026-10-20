// SPDX-License-Identifier: GPL-3.0-or-later

// Package triggers contains code and data related to triggers.
package triggers

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/testable"
	"github.com/google/uuid"
)

// ValidName contains the valid trigger names.
var ValidName = map[string]bool{
	"etl-ptnop-archive": true,
	"etl-ptnop-prom":    true,
}

// CreatedSpan models a newly created span.
type CreatedSpan struct {
	// DataType describes the data type.
	DataType string `json:"dataType"`

	// SpanID is the span identifier (UUIDv7).
	SpanID string `json:"spanId"`
}

// Directory maps triggerName to its directory.
//
// This function panics if the given trigger name is not a valid
// one: you are supposed to check for the name validity using [ValidName]
// ahead of invoking this utility function.
func Directory(runDir, triggerName string) string {
	runtimex.Assert(ValidName[triggerName])
	return filepath.Join(runDir, triggerName)
}

// SpansCollector abstracts a type that collects spanIDs.
type SpansCollector interface {
	CreatedSpans() []CreatedSpan
}

// Write writes triggers inside `$runDir/scan/` and then hardlinks
// them for any valid trigger name at `$runDir/$name/`.
func Write(
	env *testable.Environ, logger *slog.Logger, runDir string,
	runner SpansCollector, triggerNames []string) error {
	// 1. Determine whether we actually need to write triggers.
	if len(triggerNames) <= 0 {
		return nil
	}
	created := runner.CreatedSpans()
	if len(created) <= 0 {
		return nil
	}

	// 2. Determine the work unit ID.
	workUnitID := uuid.Must(uuid.NewV7()).String()

	// 3. Write the work unit inside the `$runDir/scan` directory.
	workUnitPath := filepath.Join(runDir, "scan", workUnitID+".jsonl")
	filep, err := env.OpenFile(workUnitPath, os.O_CREATE|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}
	defer env.Remove(workUnitPath)
	defer filep.Close()
	for _, entry := range created {
		data := runtimex.PanicOnError1(json.Marshal(entry))
		data = append(data, '\n')
		if _, err := filep.Write(data); err != nil {
			return err
		}
	}
	if err := filep.Close(); err != nil {
		return err
	}

	// 4. Create hard links to trigger the pipelines.
	for _, tname := range triggerNames {
		if !ValidName[tname] {
			logger.Warn(
				"trigger.ValidName",
				slog.String("err", "invalid trigger name"),
				slog.String("name", tname),
			)
			continue
		}
		tdirpath := Directory(runDir, tname)
		tfilepath := filepath.Join(tdirpath, workUnitID+".jsonl")
		if err := env.Link(workUnitPath, tfilepath); err != nil {
			return err
		}
	}
	return nil
}
