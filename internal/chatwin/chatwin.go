// Package chatwin is the chat window: a small native window (WebView2 on Windows) with the page in
// chat.html. The page calls Host through bound functions; slow ones (sending a message, checking a
// key) run on goroutines and answer the page when done, so the window never freezes.
package chatwin

import (
	"context"
	_ "embed"
	"errors"

	"mergelanes/internal/assistant"
)

// Host is what the window needs from the program.
type Host interface {
	Send(ctx context.Context, text string) (assistant.Reply, error)
	Reset()
	Status() Status
	KeySource() string // "env", "saved", or "" without a key
	SetKey(ctx context.Context, key string) error
	OpenURL(url string) error
}

// Status is the line at the top of the window.
type Status struct {
	Running  bool            `json:"running"`
	Merging  bool            `json:"merging"`
	Adapters []AdapterStatus `json:"adapters"`
}

type AdapterStatus struct {
	Name string  `json:"name"`
	Up   bool    `json:"up"`
	Mbps float64 `json:"mbps"`
}

//go:embed chat.html
var page string

// Title is the window's title, also used to find an open window.
const Title = "MergeLanes Chat"

// sendResult is what the page gets for a message: the reply, or an error code it can explain in the
// user's language.
type sendResult struct {
	Reply   *assistant.Reply   `json:"reply,omitempty"`
	Error   string             `json:"error,omitempty"` // nokey, badkey, unavailable, other
	Message string             `json:"message,omitempty"`
	Actions []assistant.Action `json:"actions,omitempty"` // changes made before the error
}

func toResult(r assistant.Reply, err error) sendResult {
	if err == nil {
		return sendResult{Reply: &r}
	}
	res := sendResult{Error: "other", Message: err.Error(), Actions: r.Actions}
	switch {
	case errors.Is(err, assistant.ErrNoKey):
		res.Error, res.Message = "nokey", ""
	case errors.Is(err, assistant.ErrBadKey):
		res.Error, res.Message = "badkey", ""
	case errors.Is(err, assistant.ErrUnavailable):
		res.Error = "unavailable"
	case errors.Is(err, context.DeadlineExceeded):
		res.Error, res.Message = "unavailable", "timed out"
	}
	return res
}

// allowedURL is the only page the window may open in the browser: where to get a key.
const allowedURL = "https://aistudio.google.com/apikey"

func errResult(err error) sendResult { return toResult(assistant.Reply{}, err) }
