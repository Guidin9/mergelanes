package assistant

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"mergelanes/internal/apps"
)

type carryBackend struct{ sets atomic.Int32 }

func (b *carryBackend) Snapshot() (Snapshot, error) {
	return Snapshot{Running: true, Merging: true,
		Adapters: []Adapter{{Name: "Ethernet", Up: true}, {Name: "Wi-Fi", Up: true}},
		Apps:     []App{{Name: "Spotify.exe"}}}, nil
}
func (b *carryBackend) SetAppLane(string, apps.Mode) (bool, error) { b.sets.Add(1); return true, nil }
func (b *carryBackend) SetMerging(bool) error                      { return nil }
func (b *carryBackend) SetLimit(string, float64) (bool, error)     { return true, nil }

// Model a makes a change and then fails; model b only answers. The change did happen, so the reply
// must still show it.
func TestActionsOfAFailedAttemptAreKept(t *testing.T) {
	var aCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		switch {
		case strings.Contains(r.URL.Path, "/models/a:"):
			if aCalls.Add(1) == 1 {
				json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"role": "model", "parts": []any{
					map[string]any{"functionCall": map[string]any{"id": "c1", "name": "set_app_lane", "args": map[string]any{"app": "Spotify.exe", "lane": "adapter", "adapter": "Wi-Fi"}}},
				}}}}})
				return
			}
			w.WriteHeader(503)
			io.WriteString(w, `{"error":{"code":503,"message":"busy"}}`)
		default:
			io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"Spotify is on Wi-Fi now."}]}}]}`)
		}
	}))
	defer srv.Close()
	b := &carryBackend{}
	g := NewGemini("k", []string{"a", "b"})
	g.BaseURL = srv.URL
	s := &Session{G: g, B: b}
	r, err := s.Send(context.Background(), "spotify wifiye")
	if err != nil {
		t.Fatal(err)
	}
	if r.Model != "b" || len(r.Actions) != 1 || !r.Actions[0].OK || r.Actions[0].Summary != "Spotify.exe → Only Wi-Fi" {
		t.Fatalf("want b's answer with a's change kept, got model=%s actions=%+v", r.Model, r.Actions)
	}
	if b.sets.Load() != 1 {
		t.Fatalf("SetAppLane calls: %d", b.sets.Load())
	}
}

func TestMergeActionsDropsRepeats(t *testing.T) {
	a := Action{Tool: "set_app_lane", Summary: "Spotify.exe → Only Wi-Fi", OK: false}
	b := Action{Tool: "set_app_lane", Summary: "Spotify.exe → Only Wi-Fi", OK: true}
	c := Action{Tool: "set_merging", Summary: "merging on", OK: true}
	got := mergeActions([]Action{a}, []Action{b, c})
	if len(got) != 2 || !got[0].OK || got[1].Tool != "set_merging" {
		t.Fatalf("got %+v", got)
	}
}
