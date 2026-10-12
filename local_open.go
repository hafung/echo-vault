package main

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"
)

func (a *App) openLocalPath(w http.ResponseWriter, r *http.Request, v M) error {
	if v["confirmed"] != true {
		return errors.New("请先确认打开本地路径")
	}
	action := str(v["action"])
	if action != "open" && action != "reveal" {
		return errors.New("打开方式无效")
	}
	target, err := localOpenTarget(str(v["path"]), action)
	if err != nil {
		return err
	}
	launch := a.openLocal
	if launch == nil {
		launch = launchLocalPath
	}
	if err = launch(r.Context(), target); err != nil {
		return fmt.Errorf("无法打开本地路径：%w", err)
	}
	jsonResponse(w, M{"ok": true})
	return nil
}

func localOpenTarget(value, action string) (string, error) {
	if strings.ContainsAny(value, "\x00\r\n") {
		return "", errors.New("本地路径无效")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("本地路径无效")
	}
	drive := len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && (value[2] == '/' || value[2] == '\\')
	if !drive && !filepath.IsAbs(value) && value != "~" && !strings.HasPrefix(value, "~/") {
		return "", errors.New("请提供本地绝对路径")
	}
	// Network shares and URL schemes are not local filesystem destinations.
	if strings.HasPrefix(value, "//") || strings.HasPrefix(value, `\\`) {
		return "", errors.New("请提供本机文件或文件夹路径")
	}
	target, err := filepath.EvalSymlinks(localPath(value))
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.New("本地文件或文件夹不存在，可能已移动或删除")
		}
		return "", fmt.Errorf("无法访问本地路径：%w", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("无法访问本地路径：%w", err)
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return "", errors.New("此路径不是普通文件或文件夹")
	}
	if action == "reveal" {
		if !info.IsDir() {
			target = filepath.Dir(target)
		}
	} else if !info.IsDir() && runnableLocalFile(target, info.Mode()) {
		return "", errors.New("此文件可能执行程序，请选择「打开所在文件夹」")
	}
	return target, nil
}

func runnableLocalFile(target string, mode os.FileMode) bool {
	switch strings.ToLower(filepath.Ext(target)) {
	case ".exe", ".com", ".bat", ".cmd", ".ps1", ".vbs", ".vbe", ".js", ".jse", ".wsf", ".wsh", ".msi", ".msp", ".scr", ".cpl", ".hta", ".lnk", ".url", ".desktop", ".sh", ".app", ".jar":
		return true
	}
	// Windows mounts in WSL often report every document as executable.
	return runtime.GOOS != "windows" && !strings.HasPrefix(target, "/mnt/") && mode&0111 != 0
}

func isWSL() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	release, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	return strings.Contains(strings.ToLower(string(release)), "microsoft")
}

func windowsOpenScript(target string) string {
	// Encode the path as data so quotes, $, backticks and newlines cannot become code.
	encoded := base64.StdEncoding.EncodeToString([]byte(target))
	return `$ErrorActionPreference='Stop'; $p=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('` + encoded + `')); $i=New-Object Diagnostics.ProcessStartInfo; $i.FileName=$p; $i.UseShellExecute=$true; [Diagnostics.Process]::Start($i) | Out-Null`
}

func encodedPowerShell(script string) string {
	units := utf16.Encode([]rune(script))
	data := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[i*2:], unit)
	}
	return base64.StdEncoding.EncodeToString(data)
}

func launchLocalPath(ctx context.Context, target string) error {
	if runtime.GOOS == "windows" || isWSL() {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if runtime.GOOS != "windows" {
			converted, err := exec.CommandContext(ctx, "wslpath", "-w", target).Output()
			if err != nil {
				return errors.New("无法将路径转换为 Windows 路径")
			}
			target = strings.TrimSpace(string(converted))
		}
		if err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodedPowerShell(windowsOpenScript(target))).Run(); err != nil {
			return errors.New("系统未能打开此路径，请检查默认应用与桌面连接")
		}
		return nil
	}
	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "open"
	}
	// Desktop launchers may wait until the viewer closes; keep the HTTP request short.
	cmd := exec.Command(command, target)
	if err := cmd.Start(); err != nil {
		return errors.New("系统文件打开程序不可用，请检查本机桌面环境")
	}
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("local file opener failed: %v", err)
		}
	}()
	return nil
}
