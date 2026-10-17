package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAssetsBesideExecutable(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "application with spaces")
	t.Chdir(t.TempDir())
	library := filepath.Join(directory, "lib", "libonnxruntime.so")
	switch runtime.GOOS {
	case "windows":
		library = filepath.Join(directory, "onnxruntime.dll")
	case "darwin":
		library = filepath.Join(directory, "lib", "libonnxruntime.dylib")
	}
	for _, path := range []string{filepath.Dir(library), filepath.Join(directory, "assets", "models", "laya")} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(library, []byte("test library"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ONNXRUNTIME_LIB", "OCR_MODELS", "AI_DECISION_MODELS_PATH", "AI_EMBEDDINGS_MODELS_PATH"} {
		t.Setenv(key, "")
	}
	t.Setenv("AI_EMBEDDINGS_PROVIDER", "local")
	for _, model := range []string{"embeddinggemma-2", "harrier-oss-v1-0.6b"} {
		t.Setenv("AI_EMBEDDINGS_MODEL", model)
		c, err := loadFrom(os.Getenv, "", directory)
		if err != nil {
			t.Fatal(err)
		}
		models := filepath.Join(directory, "assets", "models")
		if c.OCRLibrary != library || c.OCRModels != filepath.Join(models, "paddleocr") || c.OCRModelsSet ||
			c.Decisions.Models != filepath.Join(models, "laya") || c.Embeddings.Models != filepath.Join(models, "embedding", model) ||
			c.Decisions.Library != library || c.Embeddings.Library != library {
			t.Fatalf("incorrect adjacent assets: OCR %s %s, decisions %+v, embeddings %+v", c.OCRLibrary, c.OCRModels, c.Decisions, c.Embeddings)
		}
	}
	t.Setenv("ONNXRUNTIME_LIB", "custom-runtime")
	t.Setenv("OCR_MODELS", "custom-ocr")
	t.Setenv("AI_DECISION_MODELS_PATH", "custom-decisions")
	t.Setenv("AI_EMBEDDINGS_MODELS_PATH", "custom-embeddings")
	c, err := loadFrom(os.Getenv, "", directory)
	if err != nil {
		t.Fatal(err)
	}
	if c.OCRLibrary != "custom-runtime" || c.OCRModels != "custom-ocr" || !c.OCRModelsSet ||
		c.Decisions.Models != "custom-decisions" || c.Embeddings.Models != "custom-embeddings" {
		t.Fatal("explicit model configuration was overwritten")
	}
}
