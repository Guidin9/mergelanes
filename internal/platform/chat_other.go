//go:build !windows

package platform

import "errors"

// ChatWindow says the tray can open the chat window on this OS; elsewhere `mergelanes ask` is the chat.
const ChatWindow = false

func OpenChat() error {
	return errors.New("the chat window is only on Windows for now; use `mergelanes ask`")
}
