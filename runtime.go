package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// runtimeVersion is bumped whenever the bundled runtime layout changes, so
// previously extracted copies are refreshed.
const runtimeVersion = "1"

// runtimeDir is where the bundled Node.js + dsh runtime is extracted.
func runtimeDir() string {
	return filepath.Join(dshRoot(), "runtime")
}

func bundledNodeExe() string  { return filepath.Join(runtimeDir(), "node", "node.exe") }
func bundledNpmCli() string   { return filepath.Join(runtimeDir(), "node", "node_modules", "npm", "bin", "npm-cli.js") }
func bundledDshBin() string   { return filepath.Join(runtimeDir(), "dsh", "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js") }

// bundleReady reports whether the bundled runtime is present and complete.
func bundleReady() bool {
	return fileExists(bundledNodeExe()) && fileExists(bundledNpmCli()) && fileExists(bundledDshBin())
}

// ensureRuntime extracts the embedded runtime on first launch. It is a
// no-op when the runtime is already extracted or when the build carries no
// embedded runtime.
func (a *App) ensureRuntime() error {
	if len(embeddedRuntimeZip) == 0 {
		return errors.New("this build has no embedded runtime")
	}
	if bundleReady() {
		return nil
	}
	a.setStatus(Status{Kind: StatusInstalling, Text: "正在解压内置运行环境（仅首次启动，约 1-2 分钟）…"})

	dir := runtimeDir()
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	zr, err := zip.NewReader(bytes.NewReader(embeddedRuntimeZip), int64(len(embeddedRuntimeZip)))
	if err != nil {
		return err
	}
	total := len(zr.File)
	done := 0
	for _, f := range zr.File {
		if err := extractZipEntry(f, dir); err != nil {
			return err
		}
		done++
		if done%3000 == 0 {
			a.setStatus(Status{Kind: StatusInstalling,
				Text: fmt.Sprintf("正在解压内置运行环境（%d/%d 文件）…", done, total)})
		}
	}
	if !bundleReady() {
		return errors.New("内置运行环境解压不完整，请删除 " + dir + " 后重试")
	}
	if err := os.WriteFile(filepath.Join(dir, ".ok"), []byte(runtimeVersion), 0o644); err != nil {
		return err
	}
	return nil
}

func extractZipEntry(f *zip.File, dest string) error {
	path := filepath.Join(dest, filepath.FromSlash(f.Name))
	if !strings.HasPrefix(path, dest+string(filepath.Separator)) {
		return errors.New("zip entry escapes destination: " + f.Name)
	}
	if f.FileInfo().IsDir() {
		return os.MkdirAll(path, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}

// updateTargets returns the Node, npm CLI and install prefix used by the
// "update dsh" tray action (bundled runtime when active, system otherwise).
func (a *App) updateTargets() (string, string, string) {
	if len(embeddedRuntimeZip) > 0 {
		if rerr := a.ensureRuntime(); rerr == nil && bundleReady() {
			return bundledNodeExe(), bundledNpmCli(), filepath.Join(runtimeDir(), "dsh")
		}
	}
	nodePath, err := locateNode()
	if err != nil {
		return "", "", ""
	}
	npmCli, err := locateNpmCli(nodePath)
	if err != nil {
		return "", "", ""
	}
	return nodePath, npmCli, dshInstallDir()
}

// resolveRuntime picks the best Node/dsh/npm trio for this machine:
// bundled runtime first (standalone builds), then the system toolchain.
func (a *App) resolveRuntime() (nodePath, npmCli, dshBin string, err error) {
	if len(embeddedRuntimeZip) > 0 {
		if rerr := a.ensureRuntime(); rerr == nil && bundleReady() {
			nodePath = bundledNodeExe()
			npmCli = bundledNpmCli()
			dshBin = bundledDshBin()
			log.Printf("runtime: bundled node=%s", nodePath)
			return nodePath, npmCli, dshBin, nil
		} else if rerr != nil {
			log.Printf("runtime: bundled runtime unavailable (%v), falling back to system", rerr)
		}
	}

	nodePath, err = locateNode()
	if err != nil {
		return "", "", "", err
	}
	npmCli, err = locateNpmCli(nodePath)
	if err != nil {
		return "", "", "", err
	}
	dshBin, err = a.ensureDshInstalled(nodePath, npmCli)
	if err != nil {
		return "", "", "", err
	}
	log.Printf("runtime: system node=%s dsh=%s", nodePath, dshBin)
	return nodePath, npmCli, dshBin, nil
}
