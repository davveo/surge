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
	}
	if err != nil {
		return nil, fmt.Errorf("wsframe: decode: %w", err)
	}
	if env.Body == nil {
		return nil, fmt.Errorf("wsframe: empty body")
	}
	return env, nil
}

func Encode(isBinary bool, env *imv1.Envelope) ([]byte, error) {
	if isBinary {
		return proto.Marshal(env)
	}
	return protojson.Marshal(env)
}
