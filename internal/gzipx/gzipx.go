// SPDX-License-Identifier: GPL-3.0-or-later

// Package gzipx facilitates reading and writing gzipped streams.
package gzipx

import (
	"compress/gzip"
	"errors"
	"io"
	"os"
	"sync"

	"github.com/bassosimone/closepool"
	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/testable"
)

// LockedWriteCloser is a [*gzip.Writer] that ensures that no write lands after close.
//
// The purpose of this type is to ensure that writes from other goroutines landing during
// or after close fail. This outcome is better than (a) crashing inside [compress/flate] or
// (b) corrupting the gzip stream and making it impossible to read.
//
// Use [NewLockedWriteCloser] to construct.
type LockedWriteCloser struct {
	done bool
	mu   sync.Mutex
	w    *gzip.Writer
}

// NewLockedWriteCloser creates and returns a new [*LockedWriteCloser].
func NewLockedWriteCloser(w io.Writer) *LockedWriteCloser {
	return &LockedWriteCloser{
		w:  runtimex.PanicOnError1(gzip.NewWriterLevel(w, gzip.BestSpeed)),
		mu: sync.Mutex{},
	}
}

var _ io.WriteCloser = &LockedWriteCloser{}

// Close implements [io.WriteCloser].
func (w *LockedWriteCloser) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return nil
	}
	w.done = true
	return w.w.Close()
}

// ErrWriteAfterClose indicates that a write was attempted after close.
var ErrWriteAfterClose = errors.New("write after close")

// Write implements [io.Writer].
func (w *LockedWriteCloser) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return 0, ErrWriteAfterClose
	}
	return w.w.Write(data)
}

// ReadCloser is an [io.ReadCloser] for reading a gzip-compressed stream.
type ReadCloser struct {
	io.Reader
	p *closepool.Pool
}

func newReadCloser(r io.ReadCloser) (*ReadCloser, error) {
	pool := &closepool.Pool{}
	pool.Add(r)

	reader, err := gzip.NewReader(r)
	if err != nil {
		pool.Close() // make sure we close `r`
		return nil, err
	}
	pool.Add(reader)

	out := &ReadCloser{
		Reader: reader,
		p:      pool,
	}
	return out, nil
}

// NewReadCloser creates and returns a new [*ReadCloser].
func NewReadCloser(r io.Reader) (*ReadCloser, error) {
	return newReadCloser(io.NopCloser(r))
}

// OpenReadCloser opens a [*ReadCloser] for reading the given path.
func OpenReadCloser(env *testable.Environ, path string) (*ReadCloser, error) {
	filep, err := env.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	return newReadCloser(filep) // takes ownership
}

// Close implements [io.Closer].
func (rc *ReadCloser) Close() error {
	return rc.p.Close()
}
