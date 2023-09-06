package unilog

import (
	"encoding/binary"
	"fmt"
)

const totalLen = 4 + 2 + 1 + 128 + 1 + 64
const protocol = "lsh:"

type schemaBytes [totalLen]byte

func NewSchemaBytes(appName string, hostName string) schemaBytes {
	if len(appName) > 128 {
		panic("appName can be more than 128")
	}
	if len(hostName) > 64 {
		panic("HostName can be more than 64")
	}
	var s schemaBytes
	copy(s[:], protocol)                  // 4
	binary.BigEndian.PutUint16(s[4:6], 0) //2
	s[6] = byte(len(appName))             //1
	copy(s[7:], appName)                  //128
	s[135] = byte(len(hostName))
	copy(s[136:], hostName)
	return s
}

func (s *schemaBytes) String() string {
	return fmt.Sprintf("Scheme:%s, Check00:%t,AppName:%s,HostName:%s", s.Scheme(), s.Check00(), s.AppName(), s.HostName())
}
func (s *schemaBytes) Scheme() string {
	return string(s[:4])
}

func (s *schemaBytes) Check00() bool {
	return s[4] == 0 && s[5] == 0
}

func (s *schemaBytes) AppName() string {
	return string(s[7 : s[6]+7])
}
func (s *schemaBytes) HostName() string {
	return string(s[136 : s[135]+136])
}
