//go:build !windows

package chatwin

import "errors"

// Run is Windows-only for now; elsewhere the same chat works in a terminal.
func Run(Host) error {
	return errors.New("the chat window is only on Windows for now; use `mergelanes ask` in a terminal")
}
