package wsframe

import (
	"fmt"

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
		err = jsonUnmarshal.Unmarshal(data, env)
		if err != nil {
			if fixed, ok := rewriteLoneJSONSurrogates(data); ok {
				err = jsonUnmarshal.Unmarshal(fixed, env)
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
