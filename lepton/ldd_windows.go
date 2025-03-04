package lepton

import (
	"debug/elf"
	"errors"
)

// IsDynamicLinked stub
func IsDynamicLinked(efd *elf.File) bool {
	return false
}

// HasDebuggingSymbols cstub
func HasDebuggingSymbols(efd *elf.File) bool {
	return false
}

// GetElfFileInfo stub
func GetElfFileInfo(path string) (*elf.File, error) {
	return nil, errors.New("unsupported")
}

// stub
func getSharedLibs(targetRoot string, path string) (map[string]string, error) {
	var deps = make(map[string]string)
	return deps, nil
}
