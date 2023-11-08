package fsearch

import (
	"encoding/binary"
	"fmt"
)

const totalLen = 4 + 2 + 1 + 128 + 1 + 64
const protocol = "lsh:"

type schemeBytes [totalLen]byte

func NewSchemeBytes(appName string, hostName string) schemeBytes {
	if len(appName) > 128 {
		panic("appName can be more than 128")
	}
	if len(hostName) > 64 {
		panic("HostName can be more than 64")
	}
	var s schemeBytes
	copy(s[:], protocol)                  // 4
	binary.BigEndian.PutUint16(s[4:6], 0) //2
	s[6] = byte(len(appName))             //1
	copy(s[7:], appName)                  //128
	s[135] = byte(len(hostName))
	copy(s[136:], hostName)
	return s
}

func (s *schemeBytes) String() string {
	return fmt.Sprintf("Scheme:%s, CheckReserved:%t,AppName:%s,HostName:%s", s.Scheme(), s.CheckReserved(), s.AppName(), s.HostName())
}
func (s *schemeBytes) Scheme() string {
	return string(s[:4])
}

func (s *schemeBytes) CheckReserved() bool {
	return s[4] == 0 && s[5] == 0
}

func (s *schemeBytes) AppName() string {
	return string(s[7 : s[6]+7])
}
func (s *schemeBytes) HostName() string {
	return string(s[136 : s[135]+136])
}
