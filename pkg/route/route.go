package route

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

const TTL = 90 * time.Second

type Record struct {
	GatewayID string `json:"gateway_id"`
	ConnID    string `json:"conn_id"`
	DeviceID  string `json:"device_id,omitempty"`
}

func Key(uid string) string           { return "route:" + uid }
func Channel(gatewayID string) string { return "gw:" + gatewayID }

func Encode(r Record) ([]byte, error) {
	return EncodeAll([]Record{r})
}

func EncodeAll(rs []Record) ([]byte, error) {
	out := make([]Record, 0, len(rs))
	seen := map[string]struct{}{}
	for _, r := range rs {
		if r.GatewayID == "" || r.ConnID == "" {
			return nil, fmt.Errorf("route: gateway_id and conn_id required")
		}
		k := r.GatewayID + "|" + r.ConnID
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, r)
	}
	return json.Marshal(out)
}

func Decode(b []byte) (*Record, error) {
	rs, err := DecodeAll(b)
	if err != nil || len(rs) == 0 {
		return nil, err
	}
	r := rs[0]
	return &r, nil
}

func DecodeAll(b []byte) ([]Record, error) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return nil, nil
	}
	if b[0] == '[' {
		var rs []Record
		if err := json.Unmarshal(b, &rs); err != nil {
			return nil, err
		}
		return filterRecords(rs), nil
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return filterRecords([]Record{r}), nil
}

func Upsert(existing []byte, rec Record) ([]byte, error) {
	rs, err := DecodeAll(existing)
	if err != nil {
		rs = nil
	}
	out := []Record{rec}
	for _, r := range rs {
		if r.ConnID == rec.ConnID && r.GatewayID == rec.GatewayID {
			continue
		}
		out = append(out, r)
	}
	return EncodeAll(out)
}

func Remove(existing []byte, gatewayID, connID string) ([]byte, error) {
	rs, err := DecodeAll(existing)
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, r := range rs {
		if r.GatewayID == gatewayID && r.ConnID == connID {
			continue
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return EncodeAll(out)
}

func filterRecords(rs []Record) []Record {
	var out []Record
	for _, r := range rs {
		if r.GatewayID != "" && r.ConnID != "" {
			out = append(out, r)
		}
	}
	return out
}
