package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/rfdetoni/nodexlm/internal/dashboard"
)

func benchmarkCommand(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Println("nodexlm benchmark run --model MODEL --profile standard|extended|compare|capacity|stability --output FILE; benchmark compare --baseline FILE --current FILE")
		return nil
	}
	switch args[0] {
	case "run":
		return benchmarkRun(args[1:])
	case "compare":
		return benchmarkCompare(args[1:])
	default:
		return errors.New("unknown benchmark command")
	}
}

func benchmarkRun(args []string) error {
	f := fs("benchmark run")
	model := f.String("model", "", "local GGUF filename")
	profile := f.String("profile", "standard", "standard, extended, compare, capacity or stability")
	node := f.String("node", "", "Host (empty), cluster or authenticated Guest ID")
	rounds := f.Int("rounds", 3, "cluster rounds (2–10)")
	minutes := f.Int("minutes", 10, "stability duration (1–60 minutes)")
	server := f.String("server", "http://127.0.0.1:8787", "management origin")
	ca := f.String("ca", "", "trusted Host CA")
	output := f.String("output", "", "new private JSON file (never overwritten)")
	hashModel := f.Bool("hash-model", false, "stream SHA-256 of each model shard")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *model == "" || *output == "" || *rounds < 2 || *rounds > 10 || *minutes < 1 || *minutes > 60 || f.NArg() != 0 {
		return errors.New("model and output required; rounds 2–10, minutes 1–60")
	}
	if !slices.Contains([]string{"standard", "extended", "compare", "capacity", "stability"}, *profile) {
		return errors.New("invalid benchmark profile")
	}
	u, err := url.Parse(*server)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("management URL must be an origin")
	}
	if _, err := os.Stat(*output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output must be a new file")
	}
	client, err := admin(*server, *ca)
	if err != nil {
		return err
	}
	transport := client.HTTP.Transport.(*http.Transport)
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = 0
	client.HTTP.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	baseCtx, cancel := ctx()
	defer cancel()
	requestCtx, stop := context.WithTimeout(baseCtx, 100*time.Minute)
	defer stop()
	var before, after, identity json.RawMessage
	if *hashModel {
		if _, err := client.Request(requestCtx, "GET", "/api/bench/model-identity?model="+url.QueryEscape(*model), nil, &identity); err != nil {
			return err
		}
	}
	if _, err := client.Request(requestCtx, "GET", "/api/bench/metadata", nil, &before); err != nil {
		return err
	}
	var rows []map[string]any
	var baseline dashboard.InferenceSample
	single := func(target string, extended bool) (dashboard.InferenceSample, error) {
		var result dashboard.InferenceSample
		_, err := client.Request(requestCtx, "POST", "/api/bench/inference", map[string]any{"model": *model, "node_id": target, "extended": extended}, &result)
		return result, err
	}
	var basis, fingerprint, topology string
	add := func(target string, sample dashboard.InferenceSample) error {
		if sample.Kind != "benchmark" || sample.Suspect || sample.Tokens <= 0 || sample.TokensPerSecond <= 0 || math.IsNaN(sample.TokensPerSecond) || math.IsInf(sample.TokensPerSecond, 0) || len(sample.Fingerprint) != 64 || !slices.Contains([]string{"native_decode", "end_to_end"}, sample.RateBasis) {
			return errors.New("invalid benchmark sample")
		}
		if basis == "" {
			basis, fingerprint = sample.RateBasis, sample.Fingerprint
		}
		if basis != sample.RateBasis || fingerprint != sample.Fingerprint {
			return errors.New("incompatible timing basis or fingerprint")
		}
		rows = append(rows, map[string]any{"target": target, "sample": sample})
		return nil
	}
	var ratio *float64
	if *profile == "compare" || *profile == "capacity" {
		if *profile == "compare" {
			baseline, err = single("", false)
			if err != nil {
				return err
			}
			if err := add("Host", baseline); err != nil {
				return err
			}
		}
		var series []struct {
			Inference dashboard.InferenceSample `json:"inference"`
		}
		if _, err := client.Request(requestCtx, "POST", "/api/bench/inference-series", map[string]any{"model": *model, "rounds": *rounds}, &series); err != nil {
			return err
		}
		if len(series) != *rounds {
			return errors.New("incomplete cluster series")
		}
		var rates []float64
		for i, row := range series {
			s := row.Inference
			if i == 0 {
				topology = s.BenchmarkTopology
			}
			if topology == "" || s.BenchmarkTopology != topology || s.Model != *model+"@bench-cluster" {
				return errors.New("cluster topology or model changed")
			}
			if err := add(fmt.Sprintf("Cluster %d", i+1), s); err != nil {
				return err
			}
			rates = append(rates, s.TokensPerSecond)
		}
		if *profile == "compare" {
			v := benchmarkMedianRate(rates) / baseline.TokensPerSecond
			ratio = &v
		}
	} else if *profile == "stability" {
		var result dashboard.InferenceSample
		if _, err := client.Request(requestCtx, "POST", "/api/bench/stability", map[string]any{"model": *model, "node_id": *node, "minutes": *minutes}, &result); err != nil {
			return err
		}
		basis = result.RateBasis
		rows = append(rows, map[string]any{"target": benchmarkTarget(*node), "sample": result})
	} else {
		result, err := single(*node, *profile == "extended")
		if err != nil {
			return err
		}
		if err := add(benchmarkTarget(*node), result); err != nil {
			return err
		}
	}
	if _, err := client.Request(requestCtx, "GET", "/api/bench/metadata", nil, &after); err != nil {
		return err
	}
	// CLI report is intentionally private: native samples may contain device IDs.
	report := map[string]any{"schema": "nodexlm-private-benchmark-v1", "model": *model, "profile": *profile, "rate_basis": basis, "measured_at": time.Now().UTC(), "ratio": ratio, "rows": rows, "metadata_before": before, "metadata_after": after, "client_version": version, "model_identity": identity}
	file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")
	err = enc.Encode(report)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	fmt.Println("Private benchmark report:", *output)
	return nil
}

func benchmarkTarget(node string) string {
	switch node {
	case "":
		return "Host"
	case "cluster":
		return "Cluster"
	default:
		return "Guest"
	}
}

func benchmarkMedianRate(rates []float64) float64 {
	slices.Sort(rates)
	i := len(rates) / 2
	if len(rates)%2 == 0 {
		return (rates[i-1] + rates[i]) / 2
	}
	return rates[i]
}

func benchmarkCompare(args []string) error {
	f := fs("benchmark compare")
	baseline := f.String("baseline", "", "baseline private JSON")
	current := f.String("current", "", "current private JSON")
	threshold := f.Float64("threshold", 10, "allowed percentage regression")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *baseline == "" || *current == "" || math.IsNaN(*threshold) || *threshold < 0 || *threshold > 100 {
		return errors.New("baseline, current and threshold 0–100 required")
	}
	load := func(path string) (string, string, float64, error) {
		var report struct {
			Model   string `json:"model"`
			Profile string `json:"profile"`

			Rows []struct {
				Target string                    `json:"target"`
				Sample dashboard.InferenceSample `json:"sample"`
			} `json:"rows"`
		}
		info, err := os.Stat(path)
		if err != nil || info.Size() > 8<<20 {
			return "", "", 0, errors.New("invalid benchmark file")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", "", 0, err
		}
		if json.Unmarshal(raw, &report) != nil || len(report.Rows) != 1 {
			return "", "", 0, errors.New("comparison requires a single-target report")
		}
		row := report.Rows[0]
		s := row.Sample
		if s.Kind != "benchmark" || s.Suspect || s.TokensPerSecond <= 0 || s.Fingerprint == "" || !slices.Contains([]string{"native_decode", "end_to_end"}, s.RateBasis) {
			return "", "", 0, errors.New("invalid measurement")
		}
		return strings.Join([]string{report.Model, report.Profile, row.Target, s.RateBasis, s.BenchmarkTopology}, "|"), s.Fingerprint, s.TokensPerSecond, nil
	}
	a, af, av, err := load(*baseline)
	if err != nil {
		return err
	}
	b, bf, bv, err := load(*current)
	if err != nil {
		return err
	}
	if a != b || af != bf {
		return errors.New("incompatible workloads; cross-version regression requires independently verified equivalent settings and model hashes")
	}
	change := (bv/av - 1) * 100
	fmt.Printf("Baseline %.3f t/s; current %.3f t/s; change %+.2f%%\n", av, bv, change)
	if change < -*threshold {
		return errors.New("benchmark regression exceeds threshold")
	}
	return nil
}
