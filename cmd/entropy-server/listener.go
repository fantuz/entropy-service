package main

import (
	"context"
	"net"
	"syscall"
)

/*
func (l *tunedListener) Accept() (net.Conn, error) {
	conn, err := l.AcceptTCP()
	if err != nil {
		return nil, err
	}

	// Disable Nagle (TCP_NODELAY)
	conn.SetNoDelay(true)

	// Set send buffer
	if l.sndBuf > 0 {
		rawConn, err := conn.SyscallConn()
		if err == nil {
			rawConn.Control(func(fd uintptr) {
				syscall.SetsockoptInt(
					int(fd),
					syscall.SOL_SOCKET,
					syscall.SO_SNDBUF,
					l.sndBuf,
				)
			})
		}
	}

	return conn, nil
}
*/

type tunedListener struct {
	*net.TCPListener
}

func (l *tunedListener) Accept() (net.Conn, error) {
	c, err := l.TCPListener.AcceptTCP()
	if err != nil {
		return nil, err
	}

	c.SetNoDelay(true)
	c.SetWriteBuffer(4 << 20)

	return c, nil
}

func newTunedListener(addr string, sndBuf int) (net.Listener, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var ctrlErr error
			err := c.Control(func(fd uintptr) {
				// Disable Nagle (TCP_NODELAY)
				ctrlErr = syscall.SetsockoptInt(
					int(fd),
					syscall.IPPROTO_TCP,
					syscall.TCP_NODELAY,
					1,
				)
				if ctrlErr != nil {
					return
				}

				// Increase send buffer
				if sndBuf > 0 {
					ctrlErr = syscall.SetsockoptInt(
						int(fd),
						syscall.SOL_SOCKET,
						syscall.SO_SNDBUF,
						sndBuf,
					)
				}
			})
			if err != nil {
				return err
			}
			return ctrlErr
		},
	}

	return lc.Listen(context.Background(), "tcp", addr)
}

/*
func newTunedListener(addr string, sndBuf int) (net.Listener, error) {
	lc := net.ListenConfig{}

	ln, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, err
	}

	tcpLn := ln.(*net.TCPListener)

	return &tunedListener{
		TCPListener: tcpLn,
		sndBuf:      sndBuf,
	}, nil
}
*/
