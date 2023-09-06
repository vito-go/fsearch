//go:build !go1.17
// +build !go1.17

package util

import (
	"errors"
	"net"
	"strings"
)

// getInternalIP return internal ip.
func GetPrivateIP() (string, error) {
	inters, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, inter := range inters {
		if inter.Flags&net.FlagUp != 0 && !strings.HasPrefix(inter.Name, "lo") {
			addrs, err := inter.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
					if ipnet.IP.To4() != nil {
						return ipnet.IP.String(), nil
					}
				}
			}
		}
	}
	return "", errors.New("no internal ip")
}
