package main

import (
	"context"
	"fmt"

	"github.com/rfdetoni/nodexlm/internal/imagegen"
)

func generateImage(args []string) error {
	f := fs("image")
	binary := f.String("sd-bin", "sd-cli", "trusted stable-diffusion.cpp executable")
	model := f.String("model", "", "local self-contained diffusion checkpoint")
	prompt := f.String("prompt", "", "image prompt (visible to local process inspection)")
	output := f.String("output", "", "new PNG output path")
	width := f.Int("width", 512, "image width")
	height := f.Int("height", 512, "image height")
	steps := f.Int("steps", 20, "sampling steps")
	seed := f.Int64("seed", 42, "sampling seed (-1 for random)")
	if err := f.Parse(args); err != nil {
		return err
	}
	if err := imagegen.Run(context.Background(), *binary, *model, *prompt, *output, *width, *height, *steps, *seed); err != nil {
		return err
	}
	fmt.Println(*output)
	return nil
}
