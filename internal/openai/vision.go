package openai

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"
)

// Only a single bounded inline image is accepted. Never forward remote URLs.
func visionContent(raw json.RawMessage, count *int) (json.RawMessage, error) {
	if err := textContent(raw); err == nil {
		return raw, nil
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return nil, invalid("content", "Invalid multimodal content")
	}
	for _, part := range parts {
		kind, _ := stringField(part, "type")
		switch kind {
		case "text", "input_text", "output_text", "refusal":
			continue
		case "image_url":
			*count = *count + 1
			if *count > 1 {
				return nil, invalid("content", "Only one image per request is supported")
			}
			var data map[string]json.RawMessage
			if json.Unmarshal(part["image_url"], &data) != nil {
				return nil, invalid("image_url", "Invalid image object")
			}
			url, err := stringField(data, "url")
			if err != nil || !validInlineImage(url) {
				return nil, invalid("image_url", "Use an inline PNG or JPEG data URL, at most 4 MiB and 16 megapixels")
			}
			// Rebuild the object: do not let duplicate or differently-cased
			// JSON keys change the validated URL in the native parser.
			part["image_url"], _ = json.Marshal(map[string]string{"url": url})
		default:
			return nil, invalid("content", "Unsupported multimodal content part")
		}
	}
	return json.Marshal(parts)
}

func validInlineImage(raw string) bool {
	header, payload, ok := strings.Cut(raw, ",")
	if !ok || (header != "data:image/png;base64" && header != "data:image/jpeg;base64") ||
		len(payload) > base64.StdEncoding.EncodedLen(4<<20) {
		return false
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(payload)
	if err != nil || len(decoded) > 4<<20 {
		return false
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(decoded))
	return err == nil && header == "data:image/"+format+";base64" &&
		cfg.Width > 0 && cfg.Height > 0 && int64(cfg.Width) <= 16_000_000/int64(cfg.Height)
}
