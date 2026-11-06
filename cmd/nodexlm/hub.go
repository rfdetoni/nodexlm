// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/rfdetoni/nodexlm/internal/catalog"
	"github.com/rfdetoni/nodexlm/internal/clusterplan"
	"github.com/rfdetoni/nodexlm/internal/hardware"
	"github.com/rfdetoni/nodexlm/internal/protocol"
)

// hub exposes the same Hugging Face catalog/download library used by the web
// dashboard and optional desktop UI. The model still runs locally via llama.cpp.
func hub(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: nodexlm hub search|download|import")
	}
	switch args[0] {
	case "search":
		f := fs("hub search")
		q := f.String("query", "", "Hugging Face model search")
		jsonMode := f.Bool("json", false, "emit JSON recommendations")
		fit := f.Bool("fit", false, "show models estimated to fit one local node")
		server := f.String("server", "", "optional coordinator HTTPS URL to include active cluster nodes")
		ca := f.String("ca", "", "coordinator certificate file")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		items, err := (catalog.Hub{}).Search(c, *q)
		if err != nil {
			return err
		}
		name, _ := os.Hostname()
		host := hardware.Collect("Host: "+name, nil)
		host.Status = "online"
		host.LastSeen = time.Now()
		devices := []protocol.Device{host}
		if *server != "" {
			client, err := admin(*server, *ca)
			if err != nil {
				return err
			}
			var nodes []protocol.Device
			if _, err = client.Request(c, "GET", "/v1/nodes", nil, &nodes); err != nil {
				return err
			}
			devices = append(devices, nodes...)
		}
		summary := clusterplan.Aggregate(devices, time.Now())
		out := make([]clusterplan.Recommendation, 0, len(items))
		for _, item := range items {
			rec := clusterplan.Assess(summary, item)
			if *fit && rec.Placement != "single-node-candidate" {
				continue
			}
			out = append(out, rec)
		}
		if *jsonMode {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(out)
		}
		for _, item := range out {
			fmt.Printf("%-25s %6.2f GiB  %s/%s\n  %s\n", item.Placement, float64(item.SizeBytes)/float64(clusterplan.GiB), item.Repository, item.Filename, item.URL)
		}
		if len(out) == 0 {
			fmt.Println("No compatible GGUF candidates found; results are estimates only.")
		}
		return nil
	case "download":
		f := fs("hub download")
		repo := f.String("repo", "", "Hugging Face repository owner/name")
		file := f.String("file", "", "single GGUF filename inside repository")
		dir := f.String("dir", "./models", "model directory")
		name := f.String("name", "", "optional local GGUF filename")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if *repo == "" || *file == "" {
			return errors.New("--repo and --file required")
		}
		c, cancel := ctx()
		defer cancel()
		var last int64
		dest, err := (catalog.Hub{}).DownloadFile(c, *repo, *file, *dir, *name, func(n, total int64) {
			if n-last >= 16<<20 || total > 0 && n == total {
				if total > 0 {
					fmt.Fprintf(os.Stderr, "\rDownloading: %.1f%% (%d / %d MiB)\n", float64(n)*100/float64(total), n>>20, total>>20)
				} else {
					fmt.Fprintf(os.Stderr, "\rDownloading: %d MiB\n", n>>20)
				}
				last = n
			}
		})
		if err != nil {
			return err
		}
		fmt.Println(dest)
		return nil
	case "import":
		f := fs("hub import")
		source := f.String("file", "", "existing local GGUF file")
		dir := f.String("dir", "./models", "model directory")
		name := f.String("name", "", "optional destination GGUF name")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if *source == "" {
			return errors.New("--file required")
		}
		c, cancel := ctx()
		defer cancel()
		destination, err := catalog.ImportFile(c, *source, *dir, *name)
		if err != nil {
			return err
		}
		fmt.Println(destination)
		return nil
	default:
		return fmt.Errorf("unknown hub command %q", args[0])
	}
}
