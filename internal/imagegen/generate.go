package imagegen

import (
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/catalog"
)

// Run starts a trusted local sd-cli using an explicit checkpoint. It never downloads weights.
func Run(ctx context.Context, binary, model, prompt, output string, width, height, steps int, seed int64) error {
	if model == "" || prompt == "" || output == "" || len(prompt) > 8192 {
		return errors.New("--model, --prompt (max 8192 bytes) and --output required")
	}
	if err := Validate(width, height, steps, seed); err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Ext(output), ".png") {
		return errors.New("output must end in .png")
	}
	if _, err := os.Lstat(output); err == nil {
		return errors.New("output already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	info, err := os.Stat(model)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("model must be a local regular file")
	}
	ext := strings.ToLower(filepath.Ext(model))
	if ext != ".gguf" && ext != ".safetensors" && ext != ".ckpt" {
		return errors.New("unsupported diffusion checkpoint extension")
	}
	bin, err := exec.LookPath(binary)
	if err != nil {
		return errors.New("install stable-diffusion.cpp sd-cli or specify --sd-bin")
	}
	stage, err := os.MkdirTemp(filepath.Dir(output), ".nodexlm-image-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	png := filepath.Join(stage, "image.png")
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--model", model, "--prompt", prompt, "--output", png,
		"--width", strconv.Itoa(width), "--height", strconv.Itoa(height), "--steps", strconv.Itoa(steps), "--seed", strconv.FormatInt(seed, 10))
	// Avoid retaining potentially sensitive prompts in NodexLM logs.
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("sd-cli generation failed: %w", err)
	}
	info, err = os.Lstat(png)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 32<<20 {
		return errors.New("invalid diffusion output")
	}
	r, err := os.Open(png)
	if err != nil {
		return err
	}
	cfg, format, decodeErr := image.DecodeConfig(r)
	r.Close()
	if decodeErr != nil || format != "png" || cfg.Width != width || cfg.Height != height {
		return errors.New("diffusion runtime did not produce the requested PNG")
	}
	if err = catalog.PublishFile(png, output); err != nil {
		return err
	}
	return nil
}

func Validate(width, height, steps int, seed int64) error {
	if width < 64 || width > 2048 || height < 64 || height > 2048 || width%64 != 0 || height%64 != 0 ||
		width*height > 1024*1024 || steps < 1 || steps > 100 || seed < -1 {
		return errors.New("dimensions must be multiples of 64, 64..2048, at most 1 megapixel; steps 1..100 and seed >= -1")
	}
	return nil
}
