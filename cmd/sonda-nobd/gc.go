// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/plugins/nob"
	"github.com/google/uuid"
)

// Forward declarations from the importable [nob] package.
type (
	gcRequestBody  = nob.GCRequestBody
	gcResponseBody = nob.GCResponseBody
)

// GC handles `POST /api/v1/gc`.
func (h *handler) GC(w http.ResponseWriter, r *http.Request) {
	// 1. Parse request body.
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxRequestBodySize))
	decoder.DisallowUnknownFields()
	var reqb gcRequestBody
	if err := decoder.Decode(&reqb); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// 2. GC.
	if err := h.gcMain(r.Context(), reqb.MaxAge); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// 3. Send response.
	var respb gcResponseBody
	w.Write(append(runtimex.PanicOnError1(json.Marshal(respb)), '\n'))
}

// gcMain garbage collects the spool directory.
func (h *handler) gcMain(ctx context.Context, maxAge time.Duration) error {
	// Compute the cutoff time.
	cutoff := time.Now().Add(-maxAge)

	// Walk the spool sharding structure: spoolDir/XXXX/X/X/<spanID>.
	return h.gcWalkDir(h.dir, cutoff, 3)
}

// gcWalkDir walks the spool sharding tree recursively. At depth > 0,
// it descends into subdirectories and removes empty ones. At depth 0,
// it processes span directories.
func (h *handler) gcWalkDir(dir string, cutoff time.Time, depth int) error {
	entries, err := h.env.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if depth > 0 {
			child := filepath.Join(dir, e.Name())
			h.gcWalkDir(child, cutoff, depth-1)
			os.Remove(child)
		} else {
			h.gcMaybeRemoveSpan(dir, e.Name(), cutoff)
		}
	}
	return nil
}

// gcMaybeRemoveSpan removes a span directory if its UUIDv7 timestamp is older
// than the cutoff. Handles both final and .tmp directories.
func (h *handler) gcMaybeRemoveSpan(parent, name string, cutoff time.Time) {
	// Entries are UUIDv7 with an optional `.tmp` prefix if in progress; that said
	// it's fine to delete very old in progress entries.
	uuidStr := strings.TrimSuffix(name, ".tmp")
	parsedID, err := uuid.Parse(uuidStr)
	if err != nil {
		return
	}
	if parsedID.Version() != 7 {
		return
	}

	// Determine whether this entry is too new to remove.
	sec, nsec := parsedID.Time().UnixTime()
	ts := time.Unix(sec, nsec)
	if !ts.Before(cutoff) {
		return
	}

	// Remove the directory entry.
	spanPath := filepath.Join(parent, name)
	if err := h.env.RemoveAll(spanPath); err != nil {
		h.logger.Warn("rm", slog.String("path", spanPath), slog.Any("err", err))
	}
}
