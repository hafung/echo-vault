package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os/exec"
	"runtime"
	"time"
)

func gptVaultRunning(baseURL string) bool {
	client := &http.Client{Timeout: time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(baseURL + "/api/health")
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false
	}
	var health struct {
		App string `json:"app"`
	}
	return json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&health) == nil && (health.App == "gpt-vault" || health.App == "echo-vault")
}

func openArchiveBrowser(address string) error {
	err := launchArchiveBrowser(address)
	if err != nil {
		log.Printf("自动打开浏览器失败：%v；请访问 %s", err, address)
	}
	return err
}

func launchArchiveBrowser(address string) error {
	if runtime.GOOS == "windows" || isWSL() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		// A URL is already a Windows shell destination; do not pass it to wslpath.
		return exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodedPowerShell(windowsOpenScript(address))).Run()
	}
	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "open"
	}
	cmd := exec.Command(command, address)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("browser opener failed: %v", err)
		}
	}()
	return nil
}
