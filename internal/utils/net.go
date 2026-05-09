// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package utils

import (
	"fmt"
	"net"
)

// GetOutboundIP gets a preferred outbound IP address using the net.Dial trick. It prefers IPv4.
func GetOutboundIP() (string, error) {
	// Try IPv4 first.
	conn, err := net.Dial("udp", "8.8.8.8:53")
	if err == nil {
		defer conn.Close()
		return conn.LocalAddr().(*net.UDPAddr).IP.String(), nil
	}

	// If IPv4 fails, try IPv6.
	conn6, err6 := net.Dial("udp", "[2001:4860:4860::8888]:53")
	if err6 == nil {
		defer conn6.Close()
		return conn6.LocalAddr().(*net.UDPAddr).IP.String(), nil
	}

	return "", err // Return the original IPv4 error as it's the preferred one.
}

// IsPrivateIPAddr checks if the given IP address string corresponds to a private network (IPv4 or IPv6) or a loopback address.
func IsPrivateIPAddr(s string) (bool, error) {
	ip := net.ParseIP(s)
	if ip == nil {
		return false, fmt.Errorf("invalid IP address: %s", s)
	}
	return ip.IsPrivate() || ip.IsLoopback(), nil
}
