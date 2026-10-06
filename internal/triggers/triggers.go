// SPDX-License-Identifier: GPL-3.0-or-later

// Package triggers contains code and data related to triggers.
package triggers

import (
	"path/filepath"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/config"
)

// ValidName contains the valid trigger names.
var ValidName = map[string]bool{
	"etl-ptnop-qoe": true,
}

// CreatedSpan models a newly created span.
type CreatedSpan struct {
	// DataType describes the data type.
	DataType string `json:"dataType"`

	// SpanDir is the spool directory containing the span.
	SpanDir string `json:"spanDir"`

	// SpanID is the span identifier (UUIDv7).
	SpanID string `json:"spanId"`
}

// Directory maps triggerName to its directory.
//
// This function panics if the given trigger name is not a valid
// one: you are supposed to check for the name validity using [ValidName]
// ahead of invoking this utility function.
func Directory(triggerName string) string {
	runtimex.Assert(ValidName[triggerName])
	return filepath.Join(config.RunDir, triggerName)
}
