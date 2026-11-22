package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

// ChatWindow says the tray can open the chat window on this OS.
const ChatWindow = true

// OpenChat opens the chat window as the signed-in user. The tray runs elevated, and the chat, which
// holds the user's API key and runs a browser engine, must not. explorer.exe hands the start to the
// running shell, which runs it unelevated, the same way OpenURL opens the browser. Started like that
// with no arguments, the tray exe sees the running instance and opens the chat instead of a tray.
func OpenChat() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	tray := filepath.Join(filepath.Dir(exe), "mergelanes-tray.exe")
	if _, err := os.Stat(tray); err != nil {
		// A development run of mergelanes.exe without the tray build next to it: open the chat
		// directly. It runs elevated then, which installed copies never do.
		cmd := exec.Command(exe, "chat")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.DETACHED_PROCESS}
		return cmd.Start()
	}
	winDir, err := windows.GetWindowsDirectory()
	if err != nil {
		return err
	}
	cmd := exec.Command(filepath.Join(winDir, "explorer.exe"), tray)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP}
	return cmd.Start()
}
