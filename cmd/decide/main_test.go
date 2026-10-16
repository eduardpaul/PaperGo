package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestRequestValidation(t *testing.T) {
	for _, input := range []string{`{"unknown":true}`, `{"requests":[]} {}`, `{"requests":[{"state":"x","extra":true}]}`} {
		if err := run(context.Background(), nil, strings.NewReader(input), new(bytes.Buffer)); err == nil {
			t.Fatal("accepted invalid request")
		}
	}
	if err := run(context.Background(), []string{"-help"}, strings.NewReader(""), new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
}
func TestNativeDiagnostic(t *testing.T) {
	models := os.Getenv("DECISION_MODELS_PATH")
	if models == "" || os.Getenv("ONNXRUNTIME_LIB") == "" {
		if os.Getenv("PAPERGO_REQUIRE_DECISION") == "1" {
			t.Fatal("native decision bundle required")
		}
		t.Skip("native bundle not configured")
	}
	var output bytes.Buffer
	err := run(context.Background(), []string{"-models", models}, strings.NewReader(`{"requests":[{"state":"Invoice for coffee","question":"Which category best describes this document?","options":["Other document","Invoices and bills"],"type":"choice"}]}`), &output)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Model   string
		Results []struct {
			Probabilities []float64
			ActLogits     []float32 `json:"act_logits"`
		}
	}
	if err = json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(decoded.Model, "laya:") || len(decoded.Results) != 1 || len(decoded.Results[0].ActLogits) != 2 || len(decoded.Results[0].Probabilities) != 2 {
		t.Fatal(output.String())
	}
}
