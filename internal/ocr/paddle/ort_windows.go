package paddle

import "golang.org/x/sys/windows"

func openLibrary(path string) (uintptr, error) {
	handle, err := windows.LoadLibraryEx(path, 0, windows.LOAD_WITH_ALTERED_SEARCH_PATH)
	return uintptr(handle), err
}

func librarySymbol(handle uintptr, name string) (uintptr, error) {
	return windows.GetProcAddress(windows.Handle(handle), name)
}
