//go:build go1.17

package util

import (
	"errors"
	"net"
)

func GetPrivateIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.IsPrivate() {
			return ipNet.IP.String(), err
		}
	}
	return "", errors.New("no private ip")
}
