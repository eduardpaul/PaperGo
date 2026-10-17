package main

import (
	"fmt"
	"os"

	"papergo/internal/config"
	"papergo/internal/ocr/paddle"
)

// openOCR enables PaddleOCR when the ONNX Runtime library and the model
// directory are both present. The default model directory may be absent; OCR
// stays off and the process still starts. An explicit OCR_MODELS directory
// must exist, and a present directory is checked before the server listens.
func openOCR(c config.Config) (*paddle.Engine, error) {
	if c.OCRLibrary == "" {
		return nil, nil
	}
	if _, err := os.Stat(c.OCRModels); err != nil {
		if os.IsNotExist(err) && !c.OCRModelsSet {
			return nil, nil
		}
		return nil, fmt.Errorf("configure OCR: %w", err)
	}
	engine, err := paddle.New(paddle.Config{Library: c.OCRLibrary, Models: c.OCRModels, Threads: c.OCRThreads})
	if err != nil {
		return nil, fmt.Errorf("configure OCR: %w", err)
	}
	return engine, nil
}
