// SPDX-License-Identifier: GPL-3.0-or-later

package spool

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

// gzipLockedWriteCloser is a [*gzip.Writer] that ensures that no write can land after close.
//
// The purpose of this type is to ensure that writes from other goroutines landing during
// or after close fail. This outcome is better than (a) crashing inside [compress/flate] or
// (b) corrupting the gzip stream and making it impossible to read.
//
// Use [newGzipLockedWriteCloser] to construct.
type gzipLockedWriteCloser struct {
	done bool
	mu   sync.Mutex
	w    *gzip.Writer
}

// newGzipLockedWriteCloser creates and returns a new [*gzipLockedWriteCloser].
func newGzipLockedWriteCloser(w io.Writer) *gzipLockedWriteCloser {
	return &gzipLockedWriteCloser{
		w:  runtimex.PanicOnError1(gzip.NewWriterLevel(w, gzip.BestSpeed)),
		mu: sync.Mutex{},
	}
}

var _ io.WriteCloser = &gzipLockedWriteCloser{}

// Close implements [io.WriteCloser].
func (w *gzipLockedWriteCloser) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return nil
	}
	w.done = true
	return w.w.Close()
}

// errWriteAfterClose indicates that a write was attempted after close.
var errWriteAfterClose = errors.New("write after close")

// Write implements [io.Writer].
func (w *gzipLockedWriteCloser) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return 0, errWriteAfterClose
	}
	return w.w.Write(data)
}

// gzipReadCloser is an [io.ReadCloser] for reading a gzip-compressed entity.
type gzipReadCloser struct {
	io.Reader
	p *closepool.Pool
}

// openGzipReadCloser opens a [*gzipReadCloser] for reading the given path.
func openGzipReadCloser(env *testable.Environ, path string) (*gzipReadCloser, error) {
	pool := &closepool.Pool{}
	filep, err := env.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	pool.Add(filep)

	reader, err := gzip.NewReader(filep)
	if err != nil {
		pool.Close()
		return nil, err
	}
	pool.Add(reader)

	out := &gzipReadCloser{
		Reader: reader,
		p:      pool,
	}
	return out, nil
}

func (rc *gzipReadCloser) Close() error {
	return rc.p.Close()
}
