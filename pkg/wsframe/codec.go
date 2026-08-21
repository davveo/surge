package wsframe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

var jsonUnmarshal = protojson.UnmarshalOptions{DiscardUnknown: true}

func Decode(isBinary bool, data []byte) (*imv1.Envelope, error) {
	env := &imv1.Envelope{}
	var err error
	if isBinary {
		err = proto.Unmarshal(data, env)
	} else {
		err = unmarshalJSON(data, env)
		if err != nil {
			if fixed, ok := rewriteLoneJSONSurrogates(data); ok {
				err = unmarshalJSON(fixed, env)
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("wsframe: decode: %w", err)
	}
	if env.Body == nil {
		return nil, fmt.Errorf("wsframe: empty body")
	}
	return env, nil
}

func unmarshalJSON(data []byte, env *imv1.Envelope) error {
	return jsonUnmarshal.Unmarshal(rewritePayloadTypeEnums(data), env)
}

// rewritePayloadTypeEnums turns JS payload.type into a protojson enum name
// ("TEXT", "IMAGE", …). protojson.DiscardUnknown drops unknown names and the
// numeric string "1", leaving Type as TYPE_UNSPECIFIED.
func rewritePayloadTypeEnums(data []byte) []byte {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return data
	}
	sendRaw, ok := root["send"]
	if !ok {
		return data
	}
	newSend, changed := rewriteSendPayloadType(sendRaw)
	if !changed {
		return data
	}
	root["send"] = newSend
	out, err := json.Marshal(root)
	if err != nil {
		return data
	}
	return out
}

func rewriteSendPayloadType(sendRaw json.RawMessage) (json.RawMessage, bool) {
	var send map[string]json.RawMessage
	if err := json.Unmarshal(sendRaw, &send); err != nil {
		return sendRaw, false
	}
	payloadRaw, ok := send["payload"]
	if !ok {
		return sendRaw, false
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		return sendRaw, false
	}
	n := int32(0)
	if typeRaw, ok := payload["type"]; ok {
		n, _ = parsePayloadTypeJSON(typeRaw)
	}
	if n == 0 {
		n = inferPayloadType(payload)
	}
	if n == 0 {
		return sendRaw, false
	}
	name := payloadTypeJSONName(n)
	if name == "" {
		return sendRaw, false
	}
	newType, err := json.Marshal(name)
	if err != nil {
		return sendRaw, false
	}
	if typeRaw, ok := payload["type"]; ok && bytes.Equal(bytes.TrimSpace(typeRaw), newType) {
		return sendRaw, false
	}
	payload["type"] = newType
	newPayload, err := json.Marshal(payload)
	if err != nil {
		return sendRaw, false
	}
	send["payload"] = newPayload
	out, err := json.Marshal(send)
	if err != nil {
		return sendRaw, false
	}
	return out, true
}

func inferPayloadType(payload map[string]json.RawMessage) int32 {
	if sticker, ok := payload["stickerId"]; ok && len(bytes.TrimSpace(sticker)) > 2 {
		return int32(imv1.Payload_IMAGE)
	}
	if card, ok := payload["cardUid"]; ok && len(bytes.TrimSpace(card)) > 2 {
		return int32(imv1.Payload_CARD)
	}
	if card, ok := payload["card_uid"]; ok && len(bytes.TrimSpace(card)) > 2 {
		return int32(imv1.Payload_CARD)
	}
	if n := jsonArrayLen(payload["mergeItems"]); n > 0 {
		return int32(imv1.Payload_MERGE)
	}
	if n := jsonArrayLen(payload["merge_items"]); n > 0 {
		return int32(imv1.Payload_MERGE)
	}
	if mediaRaw, ok := payload["media"]; ok {
		var media map[string]json.RawMessage
		if err := json.Unmarshal(mediaRaw, &media); err == nil {
			key := jsonString(media["objectKey"])
			if key == "" {
				key = jsonString(media["object_key"])
			}
			if key != "" {
				ct := strings.ToLower(jsonString(media["contentType"]))
				if ct == "" {
					ct = strings.ToLower(jsonString(media["content_type"]))
				}
				if strings.HasPrefix(ct, "image/") {
					return int32(imv1.Payload_IMAGE)
				}
				if strings.HasPrefix(ct, "video/") {
					return int32(imv1.Payload_VIDEO)
				}
				return int32(imv1.Payload_FILE)
			}
		}
	}
	if strings.TrimSpace(jsonString(payload["text"])) != "" {
		return int32(imv1.Payload_TEXT)
	}
	return 0
}

func jsonArrayLen(raw json.RawMessage) int {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return 0
	}
	return len(arr)
}

func jsonString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}

func payloadTypeJSONName(n int32) string {
	switch imv1.Payload_Type(n) {
	case imv1.Payload_TEXT:
		return "TEXT"
	case imv1.Payload_RECALL:
		return "RECALL"
	case imv1.Payload_SYSTEM:
		return "SYSTEM"
	case imv1.Payload_IMAGE:
		return "IMAGE"
	case imv1.Payload_FILE:
		return "FILE"
	case imv1.Payload_VIDEO:
		return "VIDEO"
	case imv1.Payload_CARD:
		return "CARD"
	case imv1.Payload_MERGE:
		return "MERGE"
	default:
		return ""
	}
}

func parsePayloadTypeJSON(raw json.RawMessage) (int32, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return 0, false
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return 0, false
		}
		return parsePayloadTypeString(s)
	}
	var n float64
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, false
	}
	return int32(n), true
}

func parsePayloadTypeString(s string) (int32, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if n, err := strconv.ParseInt(s, 10, 32); err == nil {
		return int32(n), true
	}
	switch strings.ToUpper(s) {
	case "TYPE_UNSPECIFIED", "UNSPECIFIED":
		return int32(imv1.Payload_TYPE_UNSPECIFIED), true
	case "TEXT":
		return int32(imv1.Payload_TEXT), true
	case "RECALL":
		return int32(imv1.Payload_RECALL), true
	case "SYSTEM":
		return int32(imv1.Payload_SYSTEM), true
	case "IMAGE", "STICKER", "EMOJI":
		return int32(imv1.Payload_IMAGE), true
	case "FILE", "AUDIO", "VOICE":
		return int32(imv1.Payload_FILE), true
	case "VIDEO":
		return int32(imv1.Payload_VIDEO), true
	case "CARD":
		return int32(imv1.Payload_CARD), true
	case "MERGE":
		return int32(imv1.Payload_MERGE), true
	default:
		return 0, false
	}
}

// rewriteLoneJSONSurrogates replaces unpaired \uD800-\uDFFF JSON escapes with \ufffd.
// JSON.stringify of a broken UTF-16 string (e.g. emoji split by JS .split("")) emits
// a lone high surrogate like \ud83d; protojson then fails with
// invalid escape code "\",\"men" when the next bytes are ","mentionUids".
func rewriteLoneJSONSurrogates(in []byte) ([]byte, bool) {
	out := make([]byte, 0, len(in))
	changed := false
	i := 0
	for i < len(in) {
		code, ok := jsonHexEscape(in, i)
		if !ok {
			out = append(out, in[i])
			i++
			continue
		}
		if isHighSurrogate(code) {
			next, ok2 := jsonHexEscape(in, i+6)
			if ok2 && isLowSurrogate(next) {
				out = append(out, in[i:i+12]...)
				i += 12
				continue
			}
			out = append(out, `\ufffd`...)
			i += 6
			changed = true
			continue
		}
		if isLowSurrogate(code) {
			out = append(out, `\ufffd`...)
			i += 6
			changed = true
			continue
		}
		out = append(out, in[i:i+6]...)
		i += 6
	}
	return out, changed
}

func jsonHexEscape(in []byte, i int) (uint16, bool) {
	if i+6 > len(in) || in[i] != '\\' || in[i+1] != 'u' {
		return 0, false
	}
	var v uint16
	for _, c := range in[i+2 : i+6] {
		switch {
		case c >= '0' && c <= '9':
			v = v<<4 | uint16(c-'0')
		case c >= 'a' && c <= 'f':
			v = v<<4 | uint16(c-'a'+10)
		case c >= 'A' && c <= 'F':
			v = v<<4 | uint16(c-'A'+10)
		default:
			return 0, false
		}
	}
	return v, true
}

func isHighSurrogate(c uint16) bool { return c >= 0xd800 && c <= 0xdbff }
func isLowSurrogate(c uint16) bool  { return c >= 0xdc00 && c <= 0xdfff }

func Encode(isBinary bool, env *imv1.Envelope) ([]byte, error) {
	if isBinary {
		return proto.Marshal(env)
	}
	return protojson.Marshal(env)
}
