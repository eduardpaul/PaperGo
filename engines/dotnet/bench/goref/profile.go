package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"papergo/internal/decision"
	"papergo/internal/embedding"
	"papergo/internal/ocr/paddle"
)

// profileInput selects one engine and its workloads. The harness runs the
// same workloads against the .NET engine over the protocol.
type profileInput struct {
	Capability string `json:"capability"` // ocr, embed or decide
	Models     string `json:"models"`
	Model      string `json:"model"`
	Threads    int    `json:"threads"`
	Runs       int    `json:"runs"`
	// ocr: a small image loads the models, then each page is read Runs times.
	LoadImage string   `json:"load_image"`
	Pages     []string `json:"pages"`
	// embed: one short query loads the model; Short and Long are timed per
	// text; Batch is the documents of the peak measurement.
	Short []string `json:"short"`
	Long  []string `json:"long"`
	Batch []string `json:"batch"`
	// decide: Rows[0] loads the model; one row and a batch of Rows are timed.
	Rows []decision.Request `json:"rows"`
}

type profileOutput struct {
	IdleMiB        float64              `json:"idle_mib"`
	LoadedMiB      float64              `json:"loaded_mib"`
	LoadSeconds    float64              `json:"load_seconds"`
	PeakMiB        float64              `json:"peak_mib"`
	AfterUnloadMiB float64              `json:"after_unload_mib"`
	Seconds        map[string][]float64 `json:"seconds"`
}

func memory() (rss, hwm float64) {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 2 {
			continue
		}
		v, _ := strconv.ParseFloat(fields[1], 64)
		switch fields[0] {
		case "VmRSS:":
			rss = v / 1024
		case "VmHWM:":
			hwm = v / 1024
		}
	}
	return rss, hwm
}

func resetPeak() { _ = os.WriteFile("/proc/self/clear_refs", []byte("5"), 0) }

func timed(out map[string][]float64, key string, f func() error) error {
	start := time.Now()
	if err := f(); err != nil {
		return err
	}
	out[key] = append(out[key], time.Since(start).Seconds())
	return nil
}

func profile() error {
	var in profileInput
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		return err
	}
	if in.Runs == 0 {
		in.Runs = 3
	}
	ctx := context.Background()
	lib := os.Getenv("ONNXRUNTIME_LIB")
	out := profileOutput{Seconds: map[string][]float64{}}
	var load, work func() error
	var closeEngine func() error
	switch in.Capability {
	case "ocr":
		e, err := paddle.New(paddle.Config{Library: lib, Models: in.Models, Threads: in.Threads})
		if err != nil {
			return err
		}
		images := map[string]image.Image{}
		for _, p := range append([]string{in.LoadImage}, in.Pages...) {
			if images[p], err = decode(p); err != nil {
				return err
			}
		}
		load = func() error { _, err := e.Recognize(ctx, images[in.LoadImage]); return err }
		work = func() error {
			for i, p := range in.Pages {
				for range in.Runs {
					if err := timed(out.Seconds, fmt.Sprintf("page%d", i), func() error { _, err := e.Recognize(ctx, images[p]); return err }); err != nil {
						return err
					}
				}
			}
			return nil
		}
		closeEngine = e.Close
	case "embed":
		e, err := embedding.New(embedding.Config{Model: in.Model, Models: in.Models, Library: lib, Threads: in.Threads})
		if err != nil {
			return err
		}
		load = func() error { _, err := e.EmbedQuery(ctx, "hello"); return err }
		work = func() error {
			for _, set := range []struct {
				key   string
				texts []string
			}{{"short", in.Short}, {"long", in.Long}} {
				for _, t := range set.texts {
					if err := timed(out.Seconds, set.key, func() error {
						_, err := e.EmbedDocuments(ctx, []embedding.Document{{Text: t}})
						return err
					}); err != nil {
						return err
					}
				}
			}
			docs := make([]embedding.Document, len(in.Batch))
			for i, t := range in.Batch {
				docs[i] = embedding.Document{Text: t}
			}
			return timed(out.Seconds, "batch", func() error { _, err := e.EmbedDocuments(ctx, docs); return err })
		}
		closeEngine = e.Close
	case "decide":
		e, err := decision.New(decision.Config{Models: in.Models, Library: lib, Threads: in.Threads})
		if err != nil {
			return err
		}
		load = func() error { _, err := e.Decide(ctx, in.Rows[:1]); return err }
		work = func() error {
			for range in.Runs {
				if err := timed(out.Seconds, "row", func() error { _, err := e.Decide(ctx, in.Rows[:1]); return err }); err != nil {
					return err
				}
			}
			for range in.Runs {
				if err := timed(out.Seconds, "batch", func() error { _, err := e.Decide(ctx, in.Rows); return err }); err != nil {
					return err
				}
			}
			return nil
		}
		closeEngine = e.Close
	default:
		return fmt.Errorf("unknown capability %q", in.Capability)
	}
	out.IdleMiB, _ = memory()
	start := time.Now()
	if err := load(); err != nil {
		return err
	}
	out.LoadSeconds = time.Since(start).Seconds()
	out.LoadedMiB, _ = memory()
	resetPeak()
	if err := work(); err != nil {
		return err
	}
	_, out.PeakMiB = memory()
	// Close unloads as the idle timer does (models, tokenizer, malloc_trim).
	if err := closeEngine(); err != nil {
		return err
	}
	runtime.GC()
	debug.FreeOSMemory()
	out.AfterUnloadMiB, _ = memory()
	return json.NewEncoder(os.Stdout).Encode(out)
}
