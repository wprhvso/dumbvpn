//go:build windows

package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync/atomic"
	"syscall"

	"golang.org/x/sys/windows"
)

const (
	ipUnicastIf   = 31
	ipv6UnicastIf = 31
)

var physIfaceIndex atomic.Uint32

func setPhysicalInterface(index uint32) {
	physIfaceIndex.Store(index)
}

func windowsDirectDial(ctx context.Context, network, address string) (net.Conn, error) {
	idx := physIfaceIndex.Load()
	if idx == 0 {
		var d net.Dialer
		return d.DialContext(ctx, network, address)
	}

	d := net.Dialer{
		Control: func(_, addr string, c syscall.RawConn) error {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return nil
			}

			var ctlErr error
			err = c.Control(func(fd uintptr) {
				if ip.To4() != nil {
					var b [4]byte
					binary.BigEndian.PutUint32(b[:], idx)
					ctlErr = windows.SetsockoptInt(
						windows.Handle(fd),
						windows.IPPROTO_IP,
						ipUnicastIf,
						int(binary.LittleEndian.Uint32(b[:])),
					)
				} else {
					ctlErr = windows.SetsockoptInt(
						windows.Handle(fd),
						windows.IPPROTO_IPV6,
						ipv6UnicastIf,
						int(idx),
					)
				}
			})
			if err != nil {
				return err
			}
			if ctlErr != nil {
				return fmt.Errorf("set UNICAST_IF (if=%d): %w", idx, ctlErr)
			}
			return nil
		},
	}
	return d.DialContext(ctx, network, address)
}
