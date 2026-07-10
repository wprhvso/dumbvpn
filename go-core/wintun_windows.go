//go:build windows

package main

import (
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed wintun.dll
var wintunDLL []byte

func extractWintun() {
	exe, err := os.Executable()
	if err != nil {
		windowsSendLog("extractWintun: cannot resolve exe path: %v", err)
		return
	}
	dst := filepath.Join(filepath.Dir(exe), "wintun.dll")

	if fi, err := os.Stat(dst); err == nil && fi.Size() == int64(len(wintunDLL)) {
		return
	}

	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, wintunDLL, 0o644); err != nil {
		windowsSendLog("extractWintun: write failed: %v", err)
		return
	}
	if err := os.Rename(tmp, dst); err != nil {
		windowsSendLog("extractWintun: rename failed: %v", err)
		os.Remove(tmp)
		return
	}
	windowsSendLog("Extracted embedded wintun.dll to %s", dst)
}
