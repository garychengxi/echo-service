package core

import (
	"os"
	"path/filepath"
)

// GetWorkingDir 获取 working directory
func GetWorkingDir() (string, error) {
	return os.Getwd()
}

// GetExecPath 获取 bin path
func GetExecPath() (string, error) {
	return os.Executable()
}

// GetExecDir 获取 bin directory
func GetExecDir() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", nil
	}
	return filepath.Dir(p), nil
}
