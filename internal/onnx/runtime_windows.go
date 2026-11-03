//go:build windows

package onnx

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// The executable embeds ONNX Runtime with the Visual C++ runtime it needs,
// which Windows lacks until the Visual C++ Redistributable is installed.
// scripts/native.py stages them in windows-runtime: when the build reports
// no files matching windows-runtime, run python scripts/native.py.
//
//go:embed windows-runtime/manifest.json windows-runtime/*.zst
var embedded embed.FS

var embeddedRuntime = func() bundle {
	data, err := fs.Sub(embedded, "windows-runtime")
	if err == nil {
		var b bundle
		if b, err = readBundle(data); err == nil {
			return b
		}
	}
	panic("the embedded ONNX Runtime is invalid; stage it again with python scripts/native.py: " + err.Error())
}()

// EmbeddedLibrary is the path of the ONNX Runtime embedded in the
// executable, once installed in cache. Load installs it there first.
func EmbeddedLibrary(cache string) string {
	return embeddedRuntime.library(cache)
}

// prepareLibrary installs and loads the embedded runtime when path names
// it, and returns the absolute path to load.
func prepareLibrary(path string) (string, error) {
	if filepath.Base(path) != bundleLibrary || filepath.Base(filepath.Dir(path)) != embeddedRuntime.digest {
		return path, nil
	}
	cache, err := filepath.Abs(filepath.Dir(filepath.Dir(path)))
	if err != nil {
		return "", err
	}
	return embeddedRuntime.install(cache, windowsLoader{})
}

// windowsLoader loads the runtime by absolute path, with its imports
// resolved in its own directory and System32 only: never the executable's
// or the working directory, nor PATH. The Visual C++ runtime loads first,
// so ONNX Runtime binds to these modules and not to any other copy.
type windowsLoader struct{}

// open opens path sharing read access only, so it cannot be changed,
// renamed or deleted while open. A link opens as itself and does not
// match the manifest.
func (windowsLoader) open(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

// load keeps the module loaded for the life of the process.
func (windowsLoader) load(path string) error {
	_, err := windows.LoadLibraryEx(path, 0, windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	return err
}
