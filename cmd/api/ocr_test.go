package main

import (
	"os"
	"path/filepath"
	"testing"

	"papergo/internal/config"
)

func TestOpenOCRAllowsMissingDefaultModels(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "models", "paddleocr")
	engine, err := openOCR(config.Config{OCRLibrary: "libonnxruntime.so", OCRModels: missing})
	if err != nil || engine != nil {
		t.Fatalf("missing default models: engine=%v err=%v", engine, err)
	}
	t.Logf("missing default OCR models left OCR off: %s", missing)

	_, err = openOCR(config.Config{OCRLibrary: "libonnxruntime.so", OCRModels: missing, OCRModelsSet: true})
	if err == nil {
		t.Fatal("explicit missing OCR_MODELS was accepted")
	}
	t.Logf("explicit missing OCR models: %v", err)

	dir := t.TempDir()
	if err = os.WriteFile(filepath.Join(dir, "note.txt"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = openOCR(config.Config{OCRLibrary: "libonnxruntime.so", OCRModels: dir}); err == nil {
		t.Fatal("model directory without PaddleOCR files was accepted")
	}
	t.Logf("present model directory still checked: %v", err)
}
