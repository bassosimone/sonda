// SPDX-License-Identifier: GPL-3.0-or-later

package main

import "syscall"

// peerCred returns the peer credentials using SO_PEERCRED (see socket(7)).
//
// It returns either valid credentials and nil, or [unknownPeerCreds] and an error.
func peerCred(conn syscall.Conn) (peerCreds, error) {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return unknownPeerCreds, err
	}
	var (
		ucred *syscall.Ucred
		uerr  error
	)
	err = rawConn.Control(func(fd uintptr) {
		ucred, uerr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if err != nil {
		return unknownPeerCreds, err
	}
	if uerr != nil {
		return unknownPeerCreds, uerr
	}
	return peerCreds{GID: int64(ucred.Gid), PID: int64(ucred.Pid), UID: int64(ucred.Uid)}, nil
}
