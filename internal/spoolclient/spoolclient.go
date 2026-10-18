// SPDX-License-Identifier: GPL-3.0-or-later

// Package spoolclient implements a [spool] client using a Unix domain
// socket to publish temporary spans when they are closed.
package spoolclient

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/bassosimone/closepool"
	"github.com/bassosimone/runtimex"
	"github.com/bassosimone/sonda/internal/gzipx"
	"github.com/bassosimone/sonda/internal/spool"
	"github.com/bassosimone/sonda/internal/testable"
)

// NewRootDir constructs a new [spool.RootDir] using a Unix domain socket to publish
// the data inside "closed" temporary spools.
//
// This operation is lazy: remote connections are only created when needed.
func NewRootDir(env *testable.Environ, socketPath string) (spool.RootDir, error) {
	txp := &http.Transport{
		DialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
			return env.Dialer.DialContext(ctx, "unix", socketPath)
		},
		DisableCompression:     true,
		MaxIdleConnsPerHost:    2,
		MaxConnsPerHost:        2,
		IdleConnTimeout:        30 * time.Second,
		ResponseHeaderTimeout:  60 * time.Second,
		MaxResponseHeaderBytes: 1 << 20,
	}
	root := &rootDir{&http.Client{Transport: txp}}
	return root, nil
}

// rootDir models the root directory of the spool.
type rootDir struct {
	clnt *http.Client
}

var _ spool.RootDir = &rootDir{}

// GarbageCollect implements [spool.RootDir].
func (d *rootDir) GarbageCollect(cutoff time.Time, logger spool.SLogger) {
	ctx := context.Background() // TODO(bassosimone): maybe best to add this to the API?
	query := &url.Values{}
	query.Add("cutoff", cutoff.Format(time.RFC3339))
	target := &url.URL{
		Scheme:   "http",
		Host:     "sonda.sock",
		Path:     "/gc",
		RawQuery: query.Encode(),
	}
	req := runtimex.PanicOnError1(
		http.NewRequestWithContext(ctx, http.MethodPost, target.String(), http.NoBody))

	resp, err := d.clnt.Do(req)
	if err != nil {
		logger.Warn("httpClient.Do", slog.Any("err", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		logger.Warn("httpClient.Do", slog.String("err", fmt.Sprintf(
			"expected %d, got %d", http.StatusNoContent, resp.StatusCode)))
		return
	}
	io.ReadAll(resp.Body)
}

// NewDataTypeDir implements [spool.RootDir].
func (d *rootDir) NewDataTypeDir(dataType string) (spool.DataTypeDir, error) {
	err := spool.ValidateName(dataType)
	if err != nil {
		return nil, err
	}
	return &dataTypeDir{d.clnt, dataType}, nil
}

type dataTypeDir struct {
	clnt *http.Client
	name string
}

var _ spool.DataTypeDir = &dataTypeDir{}

// CreateTempSpanDir implements [spool.DataTypeDir].
func (d *dataTypeDir) CreateTempSpanDir() (spool.TempSpanDir, error) {
	tsd := &tempSpanDir{
		clnt:     d.clnt,
		closers:  &closepool.Pool{},
		dataType: d.name,
		entities: make(map[string]*bytes.Buffer),
		spanID:   spool.NewSpanUUID().String(),
	}
	return tsd, nil
}

type tempSpanDir struct {
	clnt     *http.Client
	closers  *closepool.Pool
	dataType string
	entities map[string]*bytes.Buffer
	spanID   string
}

var _ spool.TempSpanDir = &tempSpanDir{}

// Close implements [spool.TempSpanDir].
func (d *tempSpanDir) Close() error {
	return d.closers.Close()
}

// CreateEntity implements [spool.TempSpanDir].
func (d *tempSpanDir) CreateEntity(name string) (io.Writer, error) {
	if err := spool.ValidateName(name); err != nil {
		return nil, err
	}
	if _, found := d.entities[name]; found {
		return nil, fmt.Errorf("entity %q already exists", name)
	}
	buff := &bytes.Buffer{}
	gzWriteCloser := gzipx.NewLockedWriteCloser(buff)
	d.closers.Add(gzWriteCloser)
	d.entities[name] = buff
	return gzWriteCloser, nil
}

// ID implements [spool.TempSpanDir].
func (d *tempSpanDir) ID() string {
	return d.spanID
}

// Publish implements [spool.TempSpanDir].
func (d *tempSpanDir) Publish() error {
	// 1. Close all open files so that it becomes safe to read
	// the buffers because no subsequent write lands.
	if err := d.Close(); err != nil {
		return err
	}

	// 2. Create the tarball containing the HTTP request body.
	rawBody := &bytes.Buffer{}
	writer := tar.NewWriter(rawBody)
	for name, buffer := range d.entities {
		entityRawBody := buffer.Bytes()
		header := &tar.Header{
			Name: fmt.Sprintf("%s/%s/%s", d.dataType, d.ID(), name),
			Size: int64(len(entityRawBody)),
		}
		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		if _, err := writer.Write(entityRawBody); err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}

	// 3. Create the HTTP request.
	ctx := context.Background() // same consideration as before regarding the context
	target := &url.URL{
		Scheme: "http",
		Host:   "sonda.sock",
		Path:   "/span/create",
	}
	req := runtimex.PanicOnError1(
		http.NewRequestWithContext(ctx, http.MethodPost, target.String(), rawBody))
	req.Header.Set("Content-Type", "application/x-tar")

	// 4. Perform the HTTP round trip.
	resp, err := d.clnt.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("expected %d, got %d", http.StatusCreated, resp.StatusCode)
	}
	io.ReadAll(resp.Body)
	return nil
}

// OpenSpanDir implements [spool.DataTypeDir].
func (d *dataTypeDir) OpenSpanDir(spanID string) (spool.SpanDir, error) {
	parsed, err := spool.ParseSpanUUID(spanID)
	if err != nil {
		return nil, err
	}
	sd := &spanDir{
		clnt:     d.clnt,
		dataType: d.name,
		spanID:   parsed,
	}
	return sd, nil
}

type spanDir struct {
	clnt     *http.Client
	dataType string
	spanID   spool.SpanUUID
}

var _ spool.SpanDir = &spanDir{}

// Created implements [spool.SpanDir].
func (d *spanDir) Created() time.Time {
	return d.spanID.CreationTime()
}

// DataType implements [spool.SpanDir].
func (d *spanDir) DataType() string {
	return d.dataType
}

// ID implements [spool.SpanDir].
func (d *spanDir) ID() string {
	return d.spanID.String()
}

// OpenEntity implements [spool.SpanDir].
func (d *spanDir) OpenEntity(name string) (io.ReadCloser, error) {
	if err := spool.ValidateName(name); err != nil {
		return nil, err
	}
	ctx := context.Background() // same consideration as before regarding the context

	target := &url.URL{
		Scheme: "http",
		Host:   "sonda.sock",
		Path:   fmt.Sprintf("/entity/%s/%s/%s", d.dataType, d.ID(), name),
	}
	req := runtimex.PanicOnError1(
		http.NewRequestWithContext(ctx, http.MethodGet, target.String(), http.NoBody))
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := d.clnt.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		err := fmt.Errorf("expected %d, got %d", http.StatusOK, resp.StatusCode)
		if resp.StatusCode == http.StatusNotFound {
			err = fmt.Errorf("%w: %w", fs.ErrNotExist, err)
		}
		return nil, err
	}

	return gzipx.NewReadCloser(resp.Body) // takes ownership
}
