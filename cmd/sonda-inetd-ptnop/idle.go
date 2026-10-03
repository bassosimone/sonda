// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"net"
	"time"
)

// idleReader re-arms the read deadline before each Read, so the deadline
// bounds the time spent waiting for the client, not the time spent serving.
//
// This works because [bufio.Scanner] only calls Read when it needs more
// bytes, i.e., while waiting for the next request, and never while serving.
type idleReader struct {
	conn    net.Conn
	timeout time.Duration
}

func (r *idleReader) Read(p []byte) (int, error) {
	if err := r.conn.SetReadDeadline(time.Now().Add(r.timeout)); err != nil {
		return 0, err
	}
	return r.conn.Read(p)
}

// idleWriter is like [*idleReader] but for writes: it bounds the time a
// client that does not read its responses can keep us blocked.
type idleWriter struct {
	conn    net.Conn
	timeout time.Duration
}

func (w *idleWriter) Write(p []byte) (int, error) {
	if err := w.conn.SetWriteDeadline(time.Now().Add(w.timeout)); err != nil {
		return 0, err
	}
	return w.conn.Write(p)
}
