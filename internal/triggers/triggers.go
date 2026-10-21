// SPDX-License-Identifier: GPL-3.0-or-later

// Package triggers contains common triggers data.
package triggers

// CreatedSpan models a newly created span.
type CreatedSpan struct {
	// DataType describes the data type.
	DataType string `json:"dataType"`

	// SpanID is the span identifier (UUIDv7).
	SpanID string `json:"spanId"`
}
