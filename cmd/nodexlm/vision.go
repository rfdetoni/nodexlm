package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"

	"github.com/rfdetoni/nodexlm/internal/local"
)

const maxVisionImage = 4 << 20
const maxVisionPixels = 16_000_000

type chatOutput struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// The CLI accepts only explicit local regular JPEG/PNG files. DecodeConfig
// checks dimensions without allocating the full decoded image.
func visionDataURL(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > maxVisionImage {
		return "", errors.New("image must be a regular JPEG/PNG of at most 4 MiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return "", errors.New("image changed during opening")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxVisionImage+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxVisionImage {
		return "", errors.New("image exceeds 4 MiB")
	}
	return encodeVisionData(data)
}

func generateVision(ctx context.Context, engine *local.Managed, model, prompt, dataURL string) (string, error) {
	payload := map[string]any{
		"model":      model,
		"max_tokens": 512,
		"messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": prompt},
			map[string]any{"type": "image_url", "image_url": map[string]string{"url": dataURL}},
		}}},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	var output chatOutput
	err = engine.OpenAI(ctx, "/v1/chat/completions", raw, func(status int, _ http.Header, body io.Reader) error {
		if status < 200 || status >= 300 {
			return fmt.Errorf("vision runtime returned HTTP %d; check model/projector compatibility", status)
		}
		return json.NewDecoder(io.LimitReader(body, 1<<20)).Decode(&output)
	})
	if err != nil {
		return "", err
	}
	if len(output.Choices) == 0 {
		return "", errors.New("empty vision response")
	}
	return output.Choices[0].Message.Content, nil
}

func encodeVisionData(data []byte) (string, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") || cfg.Width <= 0 || cfg.Height <= 0 ||
		int64(cfg.Width) > maxVisionPixels/int64(cfg.Height) {
		return "", errors.New("invalid JPEG/PNG or image exceeds 16 megapixels")
	}
	return "data:image/" + format + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}
