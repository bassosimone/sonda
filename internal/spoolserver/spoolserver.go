// SPDX-License-Identifier: GPL-3.0-or-later

// Package spoolserver implements a server that manages the spool.
package spoolserver

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/spool"
)

// spanEntity models an entity within a [spanDir].
//
// Use [newSpanEntity] to construct.
type spanEntity struct {
	created  time.Time
	data     []byte
	dataType string
	name     string
	spanID   string
}

// newSpanEntity creates a new [*spanEntity] from a [*tar.Header].
func newSpanEntity(header *tar.Header) (*spanEntity, error) {
	entity := &spanEntity{}

	components := strings.Split(header.Name, "/")
	if count := len(components); count != 3 {
		return nil, fmt.Errorf("expected 3 components; got %d", count)
	}

	entity.dataType = components[0]
	if err := spool.ValidateName(entity.dataType); err != nil {
		return nil, err
	}

	spanUUID, err := spool.ParseSpanUUID(components[1])
	if err != nil {
		return nil, err
	}
	entity.spanID = spanUUID.String()
	entity.created = spanUUID.CreationTime()

	entity.name = components[2]
	if err := spool.ValidateName(entity.name); err != nil {
		return nil, err
	}
	return entity, nil
}

// Load loads the entity bytes from the [*tar.Reader].
func (e *spanEntity) Load(r *tar.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	e.data = data
	return nil
}

// spanDir contains information about a span.
//
// Use [newSpanDir] to construct.
type spanDir struct {
	created  time.Time
	entities map[string]*spanEntity
	dataType string
	spanID   string
}

// newSpanDir initializes and returns a [*spanDir] without entities.
func newSpanDir() *spanDir {
	return &spanDir{
		created:  time.Time{},
		entities: map[string]*spanEntity{},
		dataType: "",
		spanID:   "",
	}
}

// AddEntity tries to add a [*spanEntity] to a [*spanDir].
func (d *spanDir) AddEntity(entity *spanEntity) error {
	if d.dataType != "" && d.dataType != entity.dataType {
		return fmt.Errorf(
			"expected all entries to have datatype %q; found %q",
			d.dataType, entity.dataType)
	}
	d.dataType = entity.dataType

	if d.spanID != "" && d.spanID != entity.spanID {
		return fmt.Errorf(
			"expected all entries to have UUID %q; found %q",
			d.spanID, entity.spanID)
	}
	d.spanID = entity.spanID
	d.created = entity.created

	if _, found := d.entities[entity.name]; found {
		return fmt.Errorf("entity already exists %q", entity.name)
	}

	d.entities[entity.name] = entity
	return nil
}

// Size returns the size of all the cached entities.
func (d *spanDir) Size() uint64 {
	total := uint64(0)
	for _, entity := range d.entities {
		amount := uint64(len(entity.data))
		runtimex.Assert(total <= maxSubmissionBodySize &&
			amount <= maxSubmissionBodySize-total)
		total += amount
	}
	return total
}

// DefaultMaxMemory is the default for [Handler.MaxMemory].
const DefaultMaxMemory = 1 << 28

// Handler handles the spool requests.
//
// Use [NewHandler] to construct.
type Handler struct {
	// MaxMemory is the maximum amount of bytes we can use to cache span entities.
	//
	// Set to [DefaultMaxMemory] by [NewHandler].
	//
	// Safe to modify before the [Handler] is used by an [*http.Server].
	MaxMemory uint64

	logger *slog.Logger
	mu     sync.Mutex
	spans  map[string]*spanDir
	totMem uint64
}

// NewHandler constructs a new [*Handler] instance.
func NewHandler(logger *slog.Logger) *Handler {
	return &Handler{
		MaxMemory: DefaultMaxMemory,
		logger:    logger,
		mu:        sync.Mutex{},
		spans:     map[string]*spanDir{},
		totMem:    0,
	}
}

// Register registers the [*Handler] paths with an [*http.ServeMux].
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("GET /entity/{dataType}/{spanId}/{name}", http.HandlerFunc(h.entity))
	mux.Handle("POST /span/create", http.HandlerFunc(h.createSpan))
	mux.Handle("POST /gc", http.HandlerFunc(h.gc))
}

// maxSubmissionBodySize is the maximum submission body size we're willing to accept.
const maxSubmissionBodySize = 1 << 24

// fail is a convenience function to fail user requests.
func (h *Handler) fail(rw http.ResponseWriter, fname string, err string, status int) {
	h.logger.Warn(fname, slog.String("err", err), slog.Int64("status", int64(status)))
	rw.WriteHeader(status)
}

// addSpan adds a [*spanDir] to the tracked spans. The integer return value is zero
// on success and the nonzero status code to use in case of error.
func (h *Handler) addSpan(dir *spanDir) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// 1. Do not bother computing the total memory if the span is a duplicate.
	if _, exists := h.spans[dir.spanID]; exists {
		return http.StatusBadRequest, fmt.Errorf("span already exists: %q", dir.spanID)
	}

	// 2. Make sure we're not going over the memory limit.
	spanTotal := dir.Size()
	if h.totMem > h.MaxMemory || spanTotal > h.MaxMemory-h.totMem {
		return http.StatusInsufficientStorage, fmt.Errorf(
			"MaxMemory (%d) exceeded by totMem (%d) + spanTotal (%d)",
			h.MaxMemory, h.totMem, spanTotal)
	}
	h.totMem += spanTotal

	// 3. Finally wire the new span in.
	h.spans[dir.spanID] = dir
	return 0, nil
}

// createSpan handles a `POST /span/create` request.
func (h *Handler) createSpan(rw http.ResponseWriter, r *http.Request) {
	// 1. Prepare for reading a tar archive from the request body.
	reader := tar.NewReader(http.MaxBytesReader(rw, r.Body, maxSubmissionBodySize))
	dir := newSpanDir()

	for {
		// 2. Read each tar header and only consider regular files.
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			h.fail(rw, "createSpan", err.Error(), http.StatusBadRequest)
			return
		}
		if !header.FileInfo().Mode().IsRegular() {
			continue
		}

		// 3. Load the entity metadata from the tar archive.
		entity, err := newSpanEntity(header)
		if err != nil {
			h.fail(rw, "createSpan", err.Error(), http.StatusBadRequest)
			return
		}

		// 4. Attempt adding the entity to the span dir.
		if err := dir.AddEntity(entity); err != nil {
			h.fail(rw, "createSpan", err.Error(), http.StatusBadRequest)
			return
		}

		// 5. Read the entity bytes
		if err := entity.Load(reader); err != nil {
			h.fail(rw, "createSpan", err.Error(), http.StatusBadRequest)
			return
		}
	}

	// 6. Reject tarballs with no entries.
	if len(dir.entities) <= 0 {
		h.fail(rw, "createSpan", "no regular file entries", http.StatusBadRequest)
		return
	}
	runtimex.Assert(dir.spanID != "") // any valid entity should set the spanID

	// 7. Insert the span into the tracked spans.
	if status, err := h.addSpan(dir); err != nil {
		h.fail(rw, "createSpan", err.Error(), status)
		return
	}
	h.logger.Info(
		"createSpan",
		slog.String("dataType", dir.dataType),
		slog.Int64("httpResponseStatusCode", http.StatusCreated),
		slog.Any("names", slices.Sorted(maps.Keys(dir.entities))),
		slog.String("spanId", dir.spanID),
	)
	rw.WriteHeader(http.StatusCreated)
}

// GarbageCollect removes all spans older than cutoff and returns their IDs.
func (h *Handler) GarbageCollect(cutoff time.Time) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var expired []string
	for id, info := range h.spans {
		if info.created.Before(cutoff) {
			expired = append(expired, id)
			spanTotal := info.Size()
			runtimex.Assert(spanTotal <= h.totMem)
			h.totMem -= spanTotal
		}
	}
	for _, id := range expired {
		delete(h.spans, id)
	}
	return expired
}

// gc handles a `POST /gc` request.
func (h *Handler) gc(rw http.ResponseWriter, r *http.Request) {
	// 1. Read the cutoff and ensure the value is sane.
	cutoff := time.Now().Add(-15 * time.Minute)
	if value := r.URL.Query().Get("cutoff"); value != "" {
		desired, err := time.Parse(time.RFC3339, value)
		if err != nil {
			h.fail(rw, "gc", err.Error(), http.StatusBadRequest)
			return
		}
		if cutoff.After(desired) {
			cutoff = desired // silently refuse too aggressive cutoffs.
		}
	}

	// 2. Remove all the spans that are before the cutoff.
	expired := h.GarbageCollect(cutoff)

	// 3. Inform the client that the gc was done (fast when using memory).
	h.logger.Info("gc", slog.Any("deleted", expired))
	rw.WriteHeader(http.StatusNoContent)
}

// entity handles a `GET /entity/{dataType}/{spanId}/{name}` request.
func (h *Handler) entity(rw http.ResponseWriter, r *http.Request) {
	var (
		dataType = r.PathValue("dataType")
		name     = r.PathValue("name")
		spanID   = r.PathValue("spanId")
	)

	h.mu.Lock()
	dir, found := h.spans[spanID]
	h.mu.Unlock()
	if !found || dir.dataType != dataType {
		h.fail(rw, "entity", "span not found", http.StatusNotFound)
		return
	}

	entity, found := dir.entities[name]
	if !found {
		h.fail(rw, "entity", "entity not found", http.StatusNotFound)
		return
	}

	h.logger.Info(
		"entity",
		slog.String("dataType", dataType),
		slog.Int64("httpResponseStatusCode", http.StatusOK),
		slog.String("name", name),
		slog.String("spanId", spanID),
	)
	rw.Write(entity.data)
}
