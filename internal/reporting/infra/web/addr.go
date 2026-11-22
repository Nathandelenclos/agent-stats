package web

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// ErrRemoteAddr reports a listen address that is not loopback while remote access was not asked for.
var ErrRemoteAddr = errors.New("address is not loopback")

// ValidateAddr accepts a host:port to listen on. The index holds the text of every prompt, so unless allowRemote is
// set only loopback addresses are accepted: 127.0.0.1, ::1 or localhost, with a port. An empty host or a host name
// other than localhost cannot be proven loopback and is refused as well.
func ValidateAddr(addr string, allowRemote bool) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid --addr %q: want host:port, for example 127.0.0.1:7373", addr)
	}
	if port == "" {
		return fmt.Errorf("invalid --addr %q: missing port", addr)
	}
	if allowRemote || isLoopbackHost(host) {
		return nil
	}
	return fmt.Errorf("%w: %q would expose the index, which holds the text of your prompts, to other machines; "+
		"use a loopback address such as 127.0.0.1:7373, or pass --allow-remote if you accept that risk", ErrRemoteAddr, addr)
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
