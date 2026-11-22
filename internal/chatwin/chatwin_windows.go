package chatwin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"

	"mergelanes/internal/icon"
)

var (
	user32                 = windows.NewLazySystemDLL("user32.dll")
	procFindWindowW        = user32.NewProc("FindWindowW")
	procShowWindow         = user32.NewProc("ShowWindow")
	procSetForegroundWnd   = user32.NewProc("SetForegroundWindow")
	procSendMessageW       = user32.NewProc("SendMessageW")
	procCreateIconFromResx = user32.NewProc("CreateIconFromResourceEx")
)

const (
	swRestore   = 9
	wmSetIcon   = 0x0080
	iconSmall   = 0
	iconBig     = 1
	iconVersion = 0x00030000
)

// Run opens the chat window and blocks until it is closed. A second call while one is open only
// brings the open one to the front.
func Run(h Host) error {
	runtime.LockOSThread() // the window and its message loop live on this thread
	defer runtime.UnlockOSThread()

	name, _ := windows.UTF16PtrFromString(`Local\MergeLanesChat`)
	mu, err := windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		focusOpen()
		return nil
	}
	if mu != 0 {
		defer windows.CloseHandle(mu)
	}

	local, _ := os.UserCacheDir() // %LOCALAPPDATA%: the browser engine's own files, per user
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:  filepath.Join(local, "MergeLanes", "WebView2"),
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title: Title, Width: 440, Height: 660, Center: true,
		},
	})
	if w == nil {
		return errors.New("could not open the chat window: it needs the Microsoft Edge WebView2 Runtime (https://go.microsoft.com/fwlink/p/?LinkId=2124703)")
	}
	defer w.Destroy()
	w.SetSize(360, 420, webview2.HintMin)
	setIcon(uintptr(w.Window()))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var seq atomic.Int64
	// async runs f on a goroutine and hands its result to window.mlDone(id, result) on the window's
	// thread; the bound function returns the id at once.
	async := func(f func() any) int64 {
		id := seq.Add(1)
		go func() {
			res := f()
			b, err := json.Marshal(res)
			if err != nil {
				b, _ = json.Marshal(sendResult{Error: "other", Message: err.Error()})
			}
			w.Dispatch(func() { w.Eval(fmt.Sprintf("window.mlDone(%d, %s)", id, b)) })
		}()
		return id
	}
	w.Bind("mlSend", func(text string) int64 {
		return async(func() any {
			c, done := context.WithTimeout(ctx, 90*time.Second)
			defer done()
			return toResult(h.Send(c, text))
		})
	})
	w.Bind("mlSetKey", func(key string) int64 {
		return async(func() any {
			c, done := context.WithTimeout(ctx, 20*time.Second)
			defer done()
			if err := h.SetKey(c, key); err != nil {
				return errResult(err)
			}
			return sendResult{}
		})
	})
	w.Bind("mlStatus", func() int64 { return async(func() any { return h.Status() }) })
	w.Bind("mlKeySource", func() string { return h.KeySource() })
	w.Bind("mlReset", func() { h.Reset() })
	w.Bind("mlOpenKeyPage", func() error { return h.OpenURL(allowedURL) })
	w.Init("window.mlIcon = 'data:image/png;base64," + base64.StdEncoding.EncodeToString(icon.PNG(true)) + "';")
	if first := os.Getenv("MERGELANES_CHAT_SEND"); first != "" { // for testing: a message sent on open
		b, _ := json.Marshal(first)
		w.Init("window.mlInitial = " + string(b) + ";")
	}
	w.SetHtml(page)
	w.Run()
	return nil
}

func focusOpen() {
	t, _ := windows.UTF16PtrFromString(Title)
	hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(t)))
	if hwnd != 0 {
		procShowWindow.Call(hwnd, swRestore)
		procSetForegroundWnd.Call(hwnd)
	}
}

// setIcon gives the window the tray's icon (made in code, so it is the same everywhere).
func setIcon(hwnd uintptr) {
	png := icon.PNG(true)
	if hwnd == 0 || len(png) == 0 {
		return
	}
	hicon, _, _ := procCreateIconFromResx.Call(uintptr(unsafe.Pointer(&png[0])), uintptr(len(png)), 1, iconVersion, 0, 0, 0)
	if hicon == 0 {
		return
	}
	procSendMessageW.Call(hwnd, wmSetIcon, iconBig, hicon)
	procSendMessageW.Call(hwnd, wmSetIcon, iconSmall, hicon)
}
