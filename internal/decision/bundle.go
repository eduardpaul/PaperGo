package decision

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
)

//go:embed manifest.json
var supportedManifest []byte

// Manifest returns the exact supported artifact contract for offline installers.
func Manifest() []byte { return bytes.Clone(supportedManifest) }

func (e *Engine) verifyBundle() error {
	if e.verified {
		return nil
	}
	var want struct {
		Files map[string]string `json:"files"`
	}
	// Compare the entire manifest, not merely the fields used by the loader.
	raw, err := os.ReadFile(filepath.Join(e.cfg.Models, "manifest.json"))
	if err != nil {
		return fmt.Errorf("laya manifest: %w", err)
	}
	var a, b any
	if json.Unmarshal(supportedManifest, &a) != nil || json.Unmarshal(raw, &b) != nil || !reflect.DeepEqual(a, b) {
		return fmt.Errorf("unsupported Laya manifest; install the pinned bundle")
	}
	_ = json.Unmarshal(supportedManifest, &want)
	for name, digest := range want.Files {
		f, err := os.Open(filepath.Join(e.cfg.Models, name))
		if err != nil {
			return fmt.Errorf("laya %s: %w", name, err)
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if hex.EncodeToString(h.Sum(nil)) != digest {
			return fmt.Errorf("laya %s checksum mismatch", name)
		}
	}
	e.verified = true
	return nil
}
