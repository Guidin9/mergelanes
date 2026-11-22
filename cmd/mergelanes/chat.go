package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"mergelanes/internal/apps"
	"mergelanes/internal/assistant"
	"mergelanes/internal/chatwin"
	"mergelanes/internal/nics"
	"mergelanes/internal/platform"
	"mergelanes/internal/state"
)

// chatBackend is what the assistant sees and changes. It runs in the chat process as the signed-in
// user, not elevated, and talks to the running tray through the state folder like `mergelanes app`,
// `limit` and `on|off` do; the API key and the browser engine never run with admin rights.
type chatBackend struct{ dir string }

var errNotRunning = errors.New("MergeLanes is not running; start it from the Start menu, then try again")

func (b chatBackend) Snapshot() (assistant.Snapshot, error) {
	snap := assistant.Snapshot{Running: platform.InstanceRunning()}
	st, on := state.Read(b.dir)
	snap.Merging = on
	seen := map[string]bool{}
	if on {
		for _, n := range st.NICs {
			if !n.Present {
				continue
			}
			snap.Adapters = append(snap.Adapters, assistant.Adapter{
				Name: n.Name, Up: n.State == "up" || n.State6 == "up",
				InMbps: n.RateIn * 8 / 1e6, OutMbps: n.RateOut * 8 / 1e6,
				PingMs: n.RTTms, LimitMbps: n.LimitMbps, Games: n.Primary,
			})
		}
		for _, a := range st.Apps {
			m, _ := apps.ParseMode(a.Mode)
			snap.Apps = append(snap.Apps, assistant.App{Name: a.Name, Rule: m, LastLane: a.Lane, Flows: a.Flows, ByAdapter: a.ByAdapter, LastSeen: a.Last})
			seen[apps.Key(a.Name)] = true
		}
	} else if found, _, err := nics.DiscoverNicsDetailed(nil, nil); err == nil {
		// Paused or not running: no live numbers, but the adapters still have names to pin to.
		limits, _ := state.LoadLimits(b.dir)
		for _, n := range found {
			snap.Adapters = append(snap.Adapters, assistant.Adapter{Name: n.Name, Up: true, LimitMbps: limits[n.Name]})
		}
	}
	if rules, err := state.LoadApps(b.dir); err == nil {
		for k, m := range rules {
			if !seen[k] {
				snap.Apps = append(snap.Apps, assistant.App{Name: k + ".exe", Rule: m})
			}
		}
	}
	return snap, nil
}

func (b chatBackend) SetAppLane(app string, m apps.Mode) (bool, error) {
	if !platform.InstanceRunning() {
		return false, errNotRunning
	}
	return appApply(b.dir, app, m, true, limitWait)
}

func (b chatBackend) SetMerging(on bool) error {
	if !platform.InstanceRunning() {
		return errNotRunning
	}
	if _, now := state.Read(b.dir); now == on {
		return nil
	}
	name := "off"
	if on {
		name = "on"
	}
	return controlApply(b.dir, name)
}

func (b chatBackend) SetLimit(adapter string, mbps float64) (bool, error) {
	if !platform.InstanceRunning() {
		return false, errNotRunning
	}
	return limitApply(b.dir, adapter, mbps, true, limitWait)
}

func newSession(dir, key string) *assistant.Session {
	return &assistant.Session{G: assistant.NewGemini(key, assistant.ModelsFromEnv()), B: chatBackend{dir: dir}}
}

// cmdAsk is the chat in a terminal: one message from the arguments, or a conversation on stdin.
func cmdAsk(args []string) {
	fs := flag.NewFlagSet("ask", flag.ExitOnError)
	dir := fs.String("state-dir", state.DefaultDir(), "state directory")
	setKey := fs.Bool("set-key", false, "read a Gemini API key from stdin, check it and save it for this user")
	forget := fs.Bool("forget-key", false, "delete the saved Gemini API key")
	fs.Parse(args)

	switch {
	case *forget:
		if err := assistant.SaveKey(""); err != nil {
			die(err)
		}
		fmt.Println("The saved Gemini API key is deleted.")
		return
	case *setKey:
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		key := strings.TrimSpace(line)
		if key == "" {
			die(errors.New("no key on stdin"))
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := assistant.NewGemini(key, nil).CheckKey(ctx); err != nil {
			die(err)
		}
		if err := assistant.SaveKey(key); err != nil {
			die(err)
		}
		fmt.Println("The Gemini API key works and is saved for this user.")
		return
	}

	key, _, err := assistant.LoadKey()
	if err != nil {
		die(fmt.Errorf("%w: get one at https://aistudio.google.com/apikey, then run: mergelanes ask -set-key", err))
	}
	s := newSession(*dir, key)
	ask := func(text string) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		r, err := s.Send(ctx, text)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return
		}
		for _, a := range r.Actions {
			mark := "✓"
			if !a.OK {
				mark = "✗"
			}
			fmt.Printf("  %s %s\n", mark, a.Summary)
		}
		fmt.Println(r.Text)
		if os.Getenv("MERGELANES_CHAT_DEBUG") != "" {
			fmt.Fprintf(os.Stderr, "(model %s)\n", r.Model)
		}
	}
	if q := strings.TrimSpace(strings.Join(fs.Args(), " ")); q != "" {
		ask(q)
		return
	}
	in := bufio.NewScanner(os.Stdin)
	fmt.Print("> ")
	for in.Scan() {
		if q := strings.TrimSpace(in.Text()); q != "" {
			ask(q)
		}
		fmt.Print("> ")
	}
}

// chatHost connects the chat window to a session; the window calls it from its own goroutines.
type chatHost struct {
	dir string
	mu  sync.Mutex
	s   *assistant.Session // nil until a key is set
}

func (h *chatHost) session() *assistant.Session {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.s
}

func (h *chatHost) Send(ctx context.Context, text string) (assistant.Reply, error) {
	s := h.session()
	if s == nil {
		return assistant.Reply{}, assistant.ErrNoKey
	}
	return s.Send(ctx, text)
}

func (h *chatHost) Reset() {
	if s := h.session(); s != nil {
		s.Reset()
	}
}

func (h *chatHost) Status() chatwin.Status {
	snap, _ := chatBackend{dir: h.dir}.Snapshot()
	st := chatwin.Status{Running: snap.Running, Merging: snap.Merging}
	for _, a := range snap.Adapters {
		st.Adapters = append(st.Adapters, chatwin.AdapterStatus{Name: a.Name, Up: a.Up, Mbps: a.InMbps + a.OutMbps})
	}
	return st
}

func (h *chatHost) KeySource() string {
	_, src, err := assistant.LoadKey()
	if err != nil {
		return ""
	}
	return src
}

func (h *chatHost) SetKey(ctx context.Context, key string) error {
	key = strings.TrimSpace(key)
	if err := assistant.NewGemini(key, nil).CheckKey(ctx); err != nil {
		return err
	}
	if err := assistant.SaveKey(key); err != nil {
		return err
	}
	h.mu.Lock()
	h.s = newSession(h.dir, key)
	h.mu.Unlock()
	return nil
}

func (h *chatHost) OpenURL(url string) error { return platform.OpenURL(url) }

// cmdChat opens the chat window.
func cmdChat(args []string) {
	fs := flag.NewFlagSet("chat", flag.ExitOnError)
	dir := fs.String("state-dir", state.DefaultDir(), "state directory")
	fs.Parse(args)
	h := &chatHost{dir: *dir}
	if key, _, err := assistant.LoadKey(); err == nil {
		h.s = newSession(*dir, key)
	}
	if err := chatwin.Run(h); err != nil {
		die(err)
	}
}
