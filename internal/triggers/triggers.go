// SPDX-License-Identifier: GPL-3.0-or-later

// Package triggers contains code and data related to triggers.
package triggers

import (
	"path/filepath"

	"github.com/bassosimone/runtimex"
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
