// Package assistant is the chat that changes MergeLanes settings from plain language ("Spotify on
// Wi-Fi, Brave on Ethernet") and answers questions about them ("which connection does Brave use?").
// It talks to Google's Gemini API with function calling; what it can see and change goes through a
// Backend, so the model never touches anything a tool does not expose.
package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultModels are tried in order. Google renames and retires Gemini models often, and on the free
// tier the bigger ones are frequently overloaded (503) or time out, so the list starts with the fast
// Flash-Lite models that handle this job well, then the "latest" alias that Google keeps pointing at
// the current one, then a bigger Flash as a last resort. A model that answers 404 is skipped, and if
// every one of them is gone, Discover picks current ones from the API's model list.
var DefaultModels = []string{"gemini-3.5-flash-lite", "gemini-flash-lite-latest", "gemini-3.1-flash-lite", "gemini-3.8-flash"}

// ModelsFromEnv returns the models in MERGELANES_GEMINI_MODELS (comma separated), or DefaultModels.
func ModelsFromEnv() []string {
	var out []string
	for _, m := range strings.Split(os.Getenv("MERGELANES_GEMINI_MODELS"), ",") {
		if m = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(m), "models/")); m != "" {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return append([]string(nil), DefaultModels...)
	}
	return out
}

const defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// How long a model is left alone after it failed, by kind of failure.
const (
	coolBusy    = 30 * time.Second // 5xx, timeout: overloaded right now
	coolQuota   = time.Minute      // 429 without a retry hint
	coolBroken  = time.Hour        // 400 for this model: it does not take our request
	coolMissing = 12 * time.Hour   // 404: renamed or retired
)

// ErrBadKey means Google rejected the API key; trying other models will not help.
var ErrBadKey = errors.New("the Gemini API key was rejected")

// ErrUnavailable means every model failed or is cooling down after a failure.
var ErrUnavailable = errors.New("no Gemini model is answering right now")

// Gemini calls the generateContent endpoint, moving on to the next model when one fails.
type Gemini struct {
	Key     string
	Models  []string
	HTTP    *http.Client // nil: a client with Timeout
	BaseURL string       // "" for Google's
	Timeout time.Duration
	Hedge   time.Duration // 0: defaultHedge; negative: never send a second copy
	now     func() time.Time

	mu         sync.Mutex
	cool       map[string]time.Time // model -> usable again at
	discovered bool
}

// NewGemini makes a client for key that tries models in order.
func NewGemini(key string, models []string) *Gemini {
	return &Gemini{Key: key, Models: models, Timeout: 20 * time.Second}
}

func (g *Gemini) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

func (g *Gemini) base() string {
	if g.BaseURL != "" {
		return strings.TrimRight(g.BaseURL, "/")
	}
	return defaultBaseURL
}

func (g *Gemini) client() *http.Client {
	if g.HTTP != nil {
		return g.HTTP
	}
	return &http.Client{Timeout: g.Timeout}
}

// ready lists the models that are not cooling down, in order.
func (g *Gemini) ready() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clock()
	var out []string
	for _, m := range g.Models {
		if now.Before(g.cool[m]) {
			continue
		}
		out = append(out, m)
	}
	return out
}

func (g *Gemini) coolDown(model string, d time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cool == nil {
		g.cool = map[string]time.Time{}
	}
	g.cool[model] = g.clock().Add(d)
}

// apiError is a non-2xx answer from the API, or a transport failure (Status 0).
type apiError struct {
	Status     int
	Reason     string // e.g. "API_KEY_INVALID", from error.details
	Message    string
	RetryAfter time.Duration
}

func (e *apiError) Error() string {
	if e.Status == 0 {
		return e.Message
	}
	return fmt.Sprintf("Gemini API %d: %s", e.Status, e.Message)
}

// cooling says how long to leave the model alone after err, or 0 if the error is not the model's
// (a bad key) and the caller should stop.
func cooling(err error) time.Duration {
	var ae *apiError
	if !errors.As(err, &ae) {
		return coolBusy
	}
	switch {
	case ae.Status == 0:
		return coolBusy
	case ae.Status == 401 || ae.Status == 403 || ae.Reason == "API_KEY_INVALID" || strings.Contains(ae.Message, "API key"):
		return 0
	case ae.Status == 404:
		return coolMissing
	case ae.Status == 429:
		if ae.RetryAfter > 0 {
			return ae.RetryAfter
		}
		return coolQuota
	case ae.Status >= 500:
		return coolBusy
	}
	return coolBroken
}

func parseAPIError(status int, body []byte) *apiError {
	var env struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
			Details []struct {
				Type       string `json:"@type"`
				Reason     string `json:"reason"`
				RetryDelay string `json:"retryDelay"`
			} `json:"details"`
		} `json:"error"`
	}
	e := &apiError{Status: status}
	if json.Unmarshal(body, &env) == nil && env.Error.Message != "" {
		e.Message = env.Error.Message
		for _, d := range env.Error.Details {
			if d.Reason != "" {
				e.Reason = d.Reason
			}
			if d.RetryDelay != "" {
				if dur, err := time.ParseDuration(d.RetryDelay); err == nil {
					e.RetryAfter = dur
				}
			}
		}
	} else {
		e.Message = strings.TrimSpace(string(body))
		if len(e.Message) > 200 {
			e.Message = e.Message[:200]
		}
	}
	return e
}

func (g *Gemini) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.base()+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", g.Key) // a header, so the key never ends up in a URL or a log line
	resp, err := g.client().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &apiError{Message: "could not reach Gemini: " + redact(err.Error(), g.Key)}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, &apiError{Message: "reading Gemini's answer: " + err.Error()}
	}
	if resp.StatusCode/100 != 2 {
		return nil, parseAPIError(resp.StatusCode, b)
	}
	return b, nil
}

func redact(s, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, "<key>")
}

// CheckKey asks for the model list, which only works with a valid key.
func (g *Gemini) CheckKey(ctx context.Context) error {
	_, err := g.do(ctx, http.MethodGet, "/models?pageSize=1", nil)
	if err != nil && cooling(err) == 0 {
		return ErrBadKey
	}
	return err
}

// Discover adds current Flash-Lite and Flash models from the API's model list after the configured
// ones, once per client, for when Google has retired every model we know.
func (g *Gemini) Discover(ctx context.Context) error {
	g.mu.Lock()
	if g.discovered {
		g.mu.Unlock()
		return nil
	}
	g.discovered = true
	g.mu.Unlock()
	b, err := g.do(ctx, http.MethodGet, "/models?pageSize=1000", nil)
	if err != nil {
		return err
	}
	var list struct {
		Models []struct {
			Name    string   `json:"name"`
			Methods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.Unmarshal(b, &list); err != nil {
		return err
	}
	var lite, flash []string
	for _, m := range list.Models {
		name := strings.TrimPrefix(m.Name, "models/")
		if !contains(m.Methods, "generateContent") || !strings.Contains(name, "flash") {
			continue
		}
		skip := false
		for _, w := range []string{"image", "tts", "audio", "live", "transcribe", "robotics", "computer", "omni", "preview", "exp", "banana"} {
			if strings.Contains(name, w) {
				skip = true
			}
		}
		if skip {
			continue
		}
		if strings.Contains(name, "lite") {
			lite = append(lite, name)
		} else {
			flash = append(flash, name)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(lite))) // newest version first: names carry it
	sort.Sort(sort.Reverse(sort.StringSlice(flash)))
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, m := range append(lite, flash...) {
		if !contains(g.Models, m) {
			g.Models = append(g.Models, m)
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// generateRequest is the body of models/{model}:generateContent. Contents are kept as raw JSON so a
// model's own turn goes back exactly as it came, thought signatures included: Gemini 3 rejects a
// function call history without them.
type generateRequest struct {
	SystemInstruction *content          `json:"systemInstruction,omitempty"`
	Contents          []json.RawMessage `json:"contents"`
	Tools             []toolSet         `json:"tools,omitempty"`
	GenerationConfig  *generationConfig `json:"generationConfig,omitempty"`
}

type generationConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type part struct {
	Text             string            `json:"text,omitempty"`
	Thought          bool              `json:"thought,omitempty"`
	FunctionCall     *functionCall     `json:"functionCall,omitempty"`
	FunctionResponse *functionResponse `json:"functionResponse,omitempty"`
}

type functionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type functionResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type generateResponse struct {
	Candidates []struct {
		Content      json.RawMessage `json:"content"`
		FinishReason string          `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata struct {
		Prompt   int `json:"promptTokenCount"`
		Output   int `json:"candidatesTokenCount"`
		Thoughts int `json:"thoughtsTokenCount"`
	} `json:"usageMetadata"`
}

// debug, from MERGELANES_CHAT_DEBUG, prints each request's model, time and tokens to stderr.
var debug = os.Getenv("MERGELANES_CHAT_DEBUG") != ""

// defaultHedge is how long a request may take before the same request is sent again and the first
// answer wins. On the free tier the same request takes anywhere from one to fifteen seconds, and a
// second copy usually comes back quickly. The same model gets the copy, so thought signatures in
// its answer stay valid for the next round. It costs a second request only when the first is slow.
const defaultHedge = 5 * time.Second

// generateHedged is generate with one hedged copy after Hedge (negative: never).
func (g *Gemini) generateHedged(ctx context.Context, model string, req *generateRequest) (*generateResponse, error) {
	after := g.Hedge
	if after == 0 {
		after = defaultHedge
	}
	if after < 0 {
		return g.generate(ctx, model, req)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // the loser is cancelled
	type result struct {
		r   *generateResponse
		err error
	}
	ch := make(chan result, 2)
	run := func() {
		r, err := g.generate(ctx, model, req)
		ch <- result{r, err}
	}
	go run()
	timer := time.NewTimer(after)
	defer timer.Stop()
	inflight, hedged := 1, false
	var last error
	for {
		select {
		case x := <-ch:
			inflight--
			if x.err == nil {
				return x.r, nil
			}
			last = x.err
			if inflight == 0 { // both failed, or the first failed before a copy was sent
				return nil, last
			}
		case <-timer.C:
			if !hedged {
				hedged, inflight = true, inflight+1
				go run()
			}
		}
	}
}

func (g *Gemini) generate(ctx context.Context, model string, req *generateRequest) (*generateResponse, error) {
	start := time.Now()
	b, err := g.do(ctx, http.MethodPost, "/models/"+model+":generateContent", req)
	if err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "[gemini] %s %v: %v\n", model, time.Since(start).Round(time.Millisecond), err)
		}
		return nil, err
	}
	var resp generateResponse
	if err := json.Unmarshal(b, &resp); err != nil {
		return nil, &apiError{Status: 502, Message: "unreadable answer: " + err.Error()}
	}
	if debug {
		u := resp.UsageMetadata
		fmt.Fprintf(os.Stderr, "[gemini] %s %v: prompt=%d out=%d thoughts=%d\n", model, time.Since(start).Round(time.Millisecond), u.Prompt, u.Output, u.Thoughts)
	}
	return &resp, nil
}

func mustRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
