package route

import (
	"encoding/json"
	"fmt"
	"time"
)

const TTL = 90 * time.Second

type Record struct {
	GatewayID string `json:"gateway_id"`
	ConnID    string `json:"conn_id"`
}

func Key(uid string) string           { return "route:" + uid }
func Channel(gatewayID string) string { return "gw:" + gatewayID }

func Encode(r Record) ([]byte, error) {
	if r.GatewayID == "" || r.ConnID == "" {
		return nil, fmt.Errorf("route: gateway_id and conn_id required")
	}
	return json.Marshal(r)
}

func Decode(b []byte) (*Record, error) {
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.GatewayID == "" {
		return nil, nil
	}
	return &r, nil
}
