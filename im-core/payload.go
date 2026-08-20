package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	imv1 "github.com/davveo/surge/proto/gen/im/v1"
)

func validateSend(fromUID, clientMsgID string, payload *imv1.Payload) error {
	if fromUID == "" || clientMsgID == "" {
		return fmt.Errorf("%w: from_uid and client_msg_id required", errInvalid)
	}
	if len(clientMsgID) > 64 {
		return fmt.Errorf("%w: client_msg_id too long", errInvalid)
	}
	if payload == nil {
		return fmt.Errorf("%w: payload required", errInvalid)
	}
	if utf8.RuneCountInString(payload.Text) > 4000 {
		return fmt.Errorf("%w: text too long", errInvalid)
	}
	switch payload.Type {
	case imv1.Payload_TEXT, imv1.Payload_SYSTEM:
		if strings.TrimSpace(payload.Text) == "" {
			return fmt.Errorf("%w: empty text", errInvalid)
		}
	case imv1.Payload_IMAGE, imv1.Payload_FILE:
		if payload.Media == nil || strings.TrimSpace(payload.Media.ObjectKey) == "" {
			return fmt.Errorf("%w: media object_key required", errInvalid)
		}
		if strings.Contains(payload.Media.ObjectKey, "..") {
			return fmt.Errorf("%w: invalid object_key", errInvalid)
		}
	default:
		return fmt.Errorf("%w: unsupported payload type", errInvalid)
	}
	return nil
}

func previewOf(p *imv1.Payload) string {
	if p == nil {
		return ""
	}
	switch p.Type {
	case imv1.Payload_IMAGE:
		return "[图片]"
	case imv1.Payload_FILE:
		name := ""
		if p.Media != nil {
			name = p.Media.Filename
		}
		if name == "" {
			return "[文件]"
		}
		return clipText("[文件] "+name, 128)
	case imv1.Payload_RECALL:
		return "已撤回一条消息"
	default:
		return clipText(p.GetText(), 128)
	}
}

func marshalMedia(m *imv1.Media) string {
	if m == nil || m.ObjectKey == "" {
		return ""
	}
	b, err := json.Marshal(mediaJSON{
		ObjectKey:   m.ObjectKey,
		ThumbKey:    m.ThumbKey,
		ContentType: m.ContentType,
		Filename:    m.Filename,
		Size:        m.Size,
		Width:       m.Width,
		Height:      m.Height,
		URL:         m.Url,
		ThumbURL:    m.ThumbUrl,
	})
	if err != nil {
		return ""
	}
	return string(b)
}

func unmarshalMedia(raw string) *imv1.Media {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var m mediaJSON
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	if m.ObjectKey == "" {
		return nil
	}
	return &imv1.Media{
		ObjectKey:   m.ObjectKey,
		ThumbKey:    m.ThumbKey,
		ContentType: m.ContentType,
		Filename:    m.Filename,
		Size:        m.Size,
		Width:       m.Width,
		Height:      m.Height,
		Url:         m.URL,
		ThumbUrl:    m.ThumbURL,
	}
}

func payloadFromCols(ptype int32, text, mediaJSON string, recalled bool) *imv1.Payload {
	if recalled {
		return &imv1.Payload{Type: imv1.Payload_RECALL}
	}
	return &imv1.Payload{
		Type:  imv1.Payload_Type(ptype),
		Text:  text,
		Media: unmarshalMedia(mediaJSON),
	}
}

type mediaJSON struct {
	ObjectKey   string `json:"objectKey"`
	ThumbKey    string `json:"thumbKey"`
	ContentType string `json:"contentType"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	Width       int32  `json:"width"`
	Height      int32  `json:"height"`
	URL         string `json:"url"`
	ThumbURL    string `json:"thumbUrl"`
}
