// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// GetSpanFile handles `GET /api/v1/spans/{spanID}/{fileName}`.
func (h *handler) GetSpanFile(w http.ResponseWriter, r *http.Request) {
	// 1. Parse the raw span ID.
	spanID := r.PathValue("spanID")
	parsedID, err := uuid.Parse(spanID)
	if err != nil || parsedID.Version() != 7 {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// 2. Parse the file name.
	fileName := r.PathValue("fileName")
	switch fileName {
	case bodyBinName, exitcodeTxtName, requestJsonName, stderrTxtName, stdoutJsonName:
	default:
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	// 3. Attempt to open the given file.
	filePath := filepath.Join(pathsSpanDir(h.dir, spanID), fileName)
	filep, err := h.env.OpenFile(filePath, os.O_RDONLY, 0)
	if err != nil {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	defer filep.Close()

	// 4. Set the content-type
	var contentType string
	switch fileName {
	case exitcodeTxtName, stderrTxtName:
		contentType = "text/plain"
	case requestJsonName, stdoutJsonName:
		contentType = "application/json"
	default:
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)

	// 5. Copy the file content
	io.Copy(w, filep)
}
