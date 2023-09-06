package unilog

import "encoding/json"

type RespBody struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func (r *RespBody) ToBytes() []byte {
	b, _ := json.Marshal(r)
	return b
}
