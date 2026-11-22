package assistant

// Behaviour tests written from the assistant spec. They talk to a fake Gemini REST server
// (net/http/httptest) and a fake Backend; time is driven through the Gemini.now fake clock.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"mergelanes/internal/apps"
)

const specKey = "spec-key-5ecret-XYZ"

// ---------------------------------------------------------------------------------------------
// Fake clock

type specClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *specClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *specClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// ---------------------------------------------------------------------------------------------
// Fake backend

type laneCall struct {
	App  string
	Mode apps.Mode
}

type limitCall struct {
	Adapter string
	Mbps    float64
}

type specBackend struct {
	mu      sync.Mutex
	snap    Snapshot
	lanes   []laneCall
	merging []bool
	limits  []limitCall
}

func (b *specBackend) Snapshot() (Snapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snap, nil
}

func (b *specBackend) SetAppLane(app string, m apps.Mode) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lanes = append(b.lanes, laneCall{app, m})
	return true, nil
}

func (b *specBackend) SetMerging(on bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.merging = append(b.merging, on)
	return nil
}

func (b *specBackend) SetLimit(adapter string, mbps float64) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.limits = append(b.limits, limitCall{adapter, mbps})
	return true, nil
}

func (b *specBackend) laneCalls() []laneCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]laneCall(nil), b.lanes...)
}

func (b *specBackend) mergingCalls() []bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]bool(nil), b.merging...)
}

func (b *specBackend) limitCalls() []limitCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]limitCall(nil), b.limits...)
}

func (b *specBackend) totalSetCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.lanes) + len(b.merging) + len(b.limits)
}

func specSnapshot() Snapshot {
	now := time.Now()
	return Snapshot{
		Running: true,
		Merging: true,
		Adapters: []Adapter{
			{Name: "Ethernet", Up: true},
			{Name: "Wi-Fi", Up: true},
		},
		Apps: []App{
			{Name: "Spotify.exe", Rule: apps.Mode{Kind: apps.ModeAuto}, ByAdapter: map[string]int64{"Ethernet": 3, "Wi-Fi": 2}, LastSeen: now},
			{Name: "brave.exe", Rule: apps.Mode{Kind: apps.ModeMerge}, ByAdapter: map[string]int64{"Ethernet": 10}, LastSeen: now},
			{Name: "EpicGamesLauncher.exe", Rule: apps.Mode{Kind: apps.ModeAuto}, LastSeen: now},
			{Name: "EpicWebHelper.exe", Rule: apps.Mode{Kind: apps.ModeAuto}, LastSeen: now},
		},
	}
}

// ---------------------------------------------------------------------------------------------
// Fake Gemini REST server

type wirePart struct {
	Text             string `json:"text"`
	Thought          bool   `json:"thought"`
	ThoughtSignature string `json:"thoughtSignature"`
	FunctionCall     *struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall"`
	FunctionResponse *struct {
		ID       string         `json:"id"`
		Name     string         `json:"name"`
		Response map[string]any `json:"response"`
	} `json:"functionResponse"`
}

type wireContent struct {
	Role  string     `json:"role"`
	Parts []wirePart `json:"parts"`
}

type wireRequest struct {
	SystemInstruction *wireContent      `json:"systemInstruction"`
	Contents          []json.RawMessage `json:"contents"`
	Tools             []json.RawMessage `json:"tools"`
}

// content decodes contents[i]; a negative i counts from the end.
func (w wireRequest) content(i int) (wireContent, error) {
	if i < 0 {
		i += len(w.Contents)
	}
	var c wireContent
	if i < 0 || i >= len(w.Contents) {
		return c, errors.New("no such content")
	}
	err := json.Unmarshal(w.Contents[i], &c)
	return c, err
}

func (w wireRequest) mustContent(t *testing.T, i int) wireContent {
	t.Helper()
	c, err := w.content(i)
	if err != nil {
		t.Fatalf("contents[%d]: %v (have %d contents)", i, err, len(w.Contents))
	}
	return c
}

func plainText(c wireContent) string {
	var sb strings.Builder
	for _, p := range c.Parts {
		if !p.Thought {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

func hasFunctionParts(c wireContent) bool {
	for _, p := range c.Parts {
		if p.FunctionCall != nil || p.FunctionResponse != nil {
			return true
		}
	}
	return false
}

type apiCall struct {
	Method string
	Path   string
	URI    string // the request line target, query included
	Key    string // x-goog-api-key header
	Model  string // generateContent only
	Req    wireRequest
	Raw    []byte
}

// genHandler answers generateContent for model; n counts that model's earlier requests.
type genHandler func(model string, n int, req wireRequest) (status int, body string)

type fakeAPI struct {
	t     *testing.T
	srv   *httptest.Server
	gen   genHandler
	list  string // body of GET /models; "" answers 404
	mu    sync.Mutex
	calls []apiCall
	count map[string]int
}

func newFakeAPI(t *testing.T, gen genHandler) *fakeAPI {
	t.Helper()
	f := &fakeAPI{t: t, gen: gen, count: map[string]int{}}
	f.srv = httptest.NewServer(f)
	t.Cleanup(func() {
		f.srv.Close()
		// Key hygiene for every request in every test (spec 14).
		for _, c := range f.all() {
			if c.Key != specKey {
				t.Errorf("%s %s: x-goog-api-key = %q, want the key", c.Method, c.Path, c.Key)
			}
			if strings.Contains(c.URI, specKey) {
				t.Errorf("%s: the API key appears in the request URL %q", c.Method, c.URI)
			}
		}
	})
	return f
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c := apiCall{Method: r.Method, Path: r.URL.Path, URI: r.RequestURI, Key: r.Header.Get("x-goog-api-key"), Raw: body}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/models/") && strings.HasSuffix(r.URL.Path, ":generateContent"):
		c.Model = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/models/"), ":generateContent")
		if err := json.Unmarshal(body, &c.Req); err != nil {
			f.t.Errorf("generateContent body is not JSON: %v", err)
		}
		f.mu.Lock()
		n := f.count[c.Model]
		f.count[c.Model]++
		f.calls = append(f.calls, c)
		f.mu.Unlock()
		status, resp := f.gen(c.Model, n, c.Req)
		w.WriteHeader(status)
		io.WriteString(w, resp)
	case r.Method == http.MethodGet && r.URL.Path == "/models":
		f.mu.Lock()
		f.calls = append(f.calls, c)
		list := f.list
		f.mu.Unlock()
		if list == "" {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, errBody(404, "NOT_FOUND", "not found"))
			return
		}
		io.WriteString(w, list)
	default:
		f.mu.Lock()
		f.calls = append(f.calls, c)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, errBody(404, "NOT_FOUND", "unknown path "+r.URL.Path))
	}
}

func (f *fakeAPI) all() []apiCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]apiCall(nil), f.calls...)
}

// genCalls returns the generateContent requests for model, oldest first.
func (f *fakeAPI) genCalls(model string) []apiCall {
	var out []apiCall
	for _, c := range f.all() {
		if c.Model == model {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeAPI) genCount(model string) int { return len(f.genCalls(model)) }

func (f *fakeAPI) listCount() int {
	n := 0
	for _, c := range f.all() {
		if c.Method == http.MethodGet && c.Path == "/models" {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------------------------
// Response builders

func specJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func respWithContent(contentJSON string) string {
	return `{"candidates":[{"content":` + contentJSON + `,"finishReason":"STOP"}]}`
}

func modelContent(parts ...string) string {
	return `{"role":"model","parts":[` + strings.Join(parts, ",") + `]}`
}

func respParts(parts ...string) string { return respWithContent(modelContent(parts...)) }

func pText(s string) string    { return specJSON(map[string]any{"text": s}) }
func pThought(s string) string { return specJSON(map[string]any{"text": s, "thought": true}) }

func pCall(id, name string, args map[string]any, sig string) string {
	if args == nil {
		args = map[string]any{}
	}
	p := map[string]any{"functionCall": map[string]any{"id": id, "name": name, "args": args}}
	if sig != "" {
		p["thoughtSignature"] = sig
	}
	return specJSON(p)
}

func textReply(s string) (int, string) { return http.StatusOK, respParts(pText(s)) }

func errBody(code int, status, msg string, details ...map[string]any) string {
	e := map[string]any{"code": code, "message": msg, "status": status}
	if len(details) > 0 {
		e["details"] = details
	}
	return specJSON(map[string]any{"error": e})
}

func retryInfo(delay string) map[string]any {
	return map[string]any{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": delay}
}

func errorInfo(reason string) map[string]any {
	return map[string]any{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": reason, "domain": "googleapis.com"}
}

func overloaded() (int, string) {
	return http.StatusServiceUnavailable, errBody(503, "UNAVAILABLE", "The model is overloaded. Please try again later.")
}

func notFound(model string) (int, string) {
	return http.StatusNotFound, errBody(404, "NOT_FOUND", "models/"+model+" is not found for API version v1beta")
}

// ---------------------------------------------------------------------------------------------
// Helpers

func newSpecSession(t *testing.T, models []string, gen genHandler) (*Session, *fakeAPI, *specBackend, *specClock) {
	t.Helper()
	api := newFakeAPI(t, gen)
	g := NewGemini(specKey, models)
	g.BaseURL = api.srv.URL
	clk := &specClock{t: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)}
	g.now = clk.Now
	be := &specBackend{snap: specSnapshot()}
	return &Session{G: g, B: be}, api, be, clk
}

func specCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func hasKey(v any, key string) bool {
	switch x := v.(type) {
	case map[string]any:
		if _, ok := x[key]; ok {
			return true
		}
		for _, e := range x {
			if hasKey(e, key) {
				return true
			}
		}
	case []any:
		for _, e := range x {
			if hasKey(e, key) {
				return true
			}
		}
	}
	return false
}

// toolResult runs one message in which the model calls name(args) once (id "c1") and then answers
// "ok". It returns the functionResponse.response the model got back for the call.
func toolResult(t *testing.T, name string, args map[string]any) (map[string]any, *specBackend, Reply) {
	t.Helper()
	s, api, be, _ := newSpecSession(t, []string{"a"}, func(model string, n int, req wireRequest) (int, string) {
		if n == 0 {
			return http.StatusOK, respParts(pCall("c1", name, args, "SIG"))
		}
		return textReply("ok")
	})
	reply, err := s.Send(specCtx(t), "please do it")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	calls := api.genCalls("a")
	if len(calls) != 2 {
		t.Fatalf("model got %d requests, want 2 (call, then answer)", len(calls))
	}
	last := calls[1].Req.mustContent(t, -1)
	if last.Role != "user" {
		t.Errorf("function results content role = %q, want user", last.Role)
	}
	for _, p := range last.Parts {
		if p.FunctionResponse != nil && p.FunctionResponse.ID == "c1" {
			if p.FunctionResponse.Name != name {
				t.Errorf("functionResponse name = %q, want %q", p.FunctionResponse.Name, name)
			}
			if p.FunctionResponse.Response == nil {
				t.Fatalf("functionResponse for c1 has no response object")
			}
			return p.FunctionResponse.Response, be, reply
		}
	}
	t.Fatalf("second request has no functionResponse with id c1: %s", calls[1].Req.Contents[len(calls[1].Req.Contents)-1])
	return nil, nil, Reply{}
}

// ---------------------------------------------------------------------------------------------
// 1. Plain answer

func TestSpecPlainAnswer(t *testing.T) {
	s, api, be, _ := newSpecSession(t, []string{"m1"}, func(model string, n int, req wireRequest) (int, string) {
		return http.StatusOK, respParts(pThought("The user asks about Brave; check the state."), pText("Brave uses Ethernet."))
	})
	reply, err := s.Send(specCtx(t), "which connection does Brave use?")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if reply.Text != "Brave uses Ethernet." {
		t.Errorf("Text = %q, want %q (thought parts excluded)", reply.Text, "Brave uses Ethernet.")
	}
	if reply.Model != "m1" {
		t.Errorf("Model = %q, want m1", reply.Model)
	}
	if len(reply.Actions) != 0 {
		t.Errorf("Actions = %+v, want none", reply.Actions)
	}
	if n := be.totalSetCalls(); n != 0 {
		t.Errorf("backend got %d set calls, want 0", n)
	}
	if n := api.genCount("m1"); n != 1 {
		t.Errorf("model got %d requests, want 1", n)
	}
}

// ---------------------------------------------------------------------------------------------
// 2. Tool loop with parallel calls and thought signatures

func TestSpecToolLoopParallelCalls(t *testing.T) {
	first := modelContent(
		pCall("c1", "set_app_lane", map[string]any{"app": "Spotify.exe", "lane": "adapter", "adapter": "wifi"}, "SIG_ONE"),
		pCall("c2", "set_app_lane", map[string]any{"app": "brave.exe", "lane": "adapter", "adapter": "Ethernet"}, "SIG_TWO"),
	)
	s, api, be, _ := newSpecSession(t, []string{"a"}, func(model string, n int, req wireRequest) (int, string) {
		if n == 0 {
			return http.StatusOK, respWithContent(first)
		}
		return textReply("Spotify is on Wi-Fi and Brave on Ethernet.")
	})
	reply, err := s.Send(specCtx(t), "Spotify on wifi, Brave on Ethernet")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if reply.Text != "Spotify is on Wi-Fi and Brave on Ethernet." {
		t.Errorf("Text = %q", reply.Text)
	}

	got := be.laneCalls()
	want := map[laneCall]bool{
		{"Spotify.exe", apps.Mode{Kind: apps.ModeAdapter, Adapter: "Wi-Fi"}}:  true,
		{"brave.exe", apps.Mode{Kind: apps.ModeAdapter, Adapter: "Ethernet"}}: true,
	}
	if len(got) != 2 {
		t.Errorf("SetAppLane calls = %+v, want exactly 2", got)
	}
	for _, c := range got {
		if !want[c] {
			t.Errorf("unexpected SetAppLane call %+v", c)
		}
		delete(want, c)
	}
	for c := range want {
		t.Errorf("missing SetAppLane call %+v", c)
	}

	if len(reply.Actions) != 2 {
		t.Errorf("Actions = %+v, want 2", reply.Actions)
	}
	for _, a := range reply.Actions {
		if !a.OK {
			t.Errorf("action %+v not OK", a)
		}
	}

	calls := api.genCalls("a")
	if len(calls) != 2 {
		t.Fatalf("model got %d requests, want 2", len(calls))
	}
	second := calls[1].Req
	if len(second.Contents) < 3 {
		t.Fatalf("second request has %d contents, want user text, model turn, function results", len(second.Contents))
	}
	echoed := second.Contents[len(second.Contents)-2]
	if !jsonEqual(echoed, []byte(first)) {
		t.Errorf("model turn not echoed verbatim:\n got %s\nwant %s", echoed, first)
	}
	for _, sig := range []string{"SIG_ONE", "SIG_TWO"} {
		if !strings.Contains(string(echoed), sig) {
			t.Errorf("echoed model turn lacks thoughtSignature %s", sig)
		}
	}
	results := second.mustContent(t, -1)
	if results.Role != "user" {
		t.Errorf("function results role = %q, want user", results.Role)
	}
	ids := map[string]string{}
	for _, p := range results.Parts {
		if p.FunctionResponse == nil {
			t.Errorf("function results content has a non-functionResponse part: %+v", p)
			continue
		}
		ids[p.FunctionResponse.ID] = p.FunctionResponse.Name
		if _, bad := p.FunctionResponse.Response["error"]; bad {
			t.Errorf("functionResponse %s is an error: %v", p.FunctionResponse.ID, p.FunctionResponse.Response)
		}
	}
	if !reflect.DeepEqual(ids, map[string]string{"c1": "set_app_lane", "c2": "set_app_lane"}) {
		t.Errorf("functionResponse ids/names = %v, want c1 and c2 for set_app_lane", ids)
	}
	if first := second.mustContent(t, 0); first.Role != "user" || plainText(first) != "Spotify on wifi, Brave on Ethernet" {
		t.Errorf("second request does not start with the user text: %+v", first)
	}
}

// ---------------------------------------------------------------------------------------------
// 3. App and adapter name resolution

func TestSpecAppResolutionCaseAndExe(t *testing.T) {
	resp, be, _ := toolResult(t, "set_app_lane", map[string]any{"app": "spotify", "lane": "merge"})
	if _, bad := resp["error"]; bad {
		t.Errorf("got error result %v", resp)
	}
	want := []laneCall{{"Spotify.exe", apps.Mode{Kind: apps.ModeMerge}}}
	if got := be.laneCalls(); !reflect.DeepEqual(got, want) {
		t.Errorf("SetAppLane calls = %+v, want %+v", got, want)
	}
}

func TestSpecAppResolutionAmbiguousPrefix(t *testing.T) {
	resp, be, _ := toolResult(t, "set_app_lane", map[string]any{"app": "epic", "lane": "merge"})
	if _, ok := resp["error"]; !ok {
		t.Errorf("ambiguous name: response %v has no error key", resp)
	}
	if got := be.laneCalls(); len(got) != 0 {
		t.Errorf("ambiguous name reached the backend: %+v", got)
	}
}

func TestSpecAppResolutionUnknownExeAccepted(t *testing.T) {
	resp, be, _ := toolResult(t, "set_app_lane", map[string]any{"app": "NewGame.exe", "lane": "fastest"})
	if _, bad := resp["error"]; bad {
		t.Errorf("unknown .exe: got error result %v", resp)
	}
	want := []laneCall{{"NewGame.exe", apps.Mode{Kind: apps.ModeFastest}}}
	if got := be.laneCalls(); !reflect.DeepEqual(got, want) {
		t.Errorf("SetAppLane calls = %+v, want %+v", got, want)
	}
	if !hasKey(resp, "note") {
		t.Errorf("unknown .exe accepted without a note: %v", resp)
	}
}

func TestSpecAppResolutionUnknownPlainName(t *testing.T) {
	resp, be, _ := toolResult(t, "set_app_lane", map[string]any{"app": "photoshop", "lane": "merge"})
	if _, ok := resp["error"]; !ok {
		t.Errorf("unknown plain name: response %v has no error key", resp)
	}
	if got := be.laneCalls(); len(got) != 0 {
		t.Errorf("unknown plain name reached the backend: %+v", got)
	}
}

func TestSpecUnknownAdapter(t *testing.T) {
	resp, be, _ := toolResult(t, "set_app_lane", map[string]any{"app": "Spotify.exe", "lane": "adapter", "adapter": "5G modem"})
	if _, ok := resp["error"]; !ok {
		t.Errorf("unknown adapter: response %v has no error key", resp)
	}
	if got := be.laneCalls(); len(got) != 0 {
		t.Errorf("unknown adapter reached the backend: %+v", got)
	}
}

// ---------------------------------------------------------------------------------------------
// 4. set_merging and set_speed_limit

func TestSpecSetMerging(t *testing.T) {
	resp, be, reply := toolResult(t, "set_merging", map[string]any{"on": true})
	if _, bad := resp["error"]; bad {
		t.Errorf("got error result %v", resp)
	}
	if got := be.mergingCalls(); !reflect.DeepEqual(got, []bool{true}) {
		t.Errorf("SetMerging calls = %v, want [true]", got)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].OK {
		t.Errorf("Actions = %+v, want one OK action", reply.Actions)
	}
}

func TestSpecSetSpeedLimit(t *testing.T) {
	resp, be, _ := toolResult(t, "set_speed_limit", map[string]any{"adapter": "wi-fi", "mbps": 5})
	if _, bad := resp["error"]; bad {
		t.Errorf("got error result %v", resp)
	}
	if got := be.limitCalls(); !reflect.DeepEqual(got, []limitCall{{"Wi-Fi", 5}}) {
		t.Errorf("SetLimit calls = %+v, want [{Wi-Fi 5}]", got)
	}
}

func TestSpecSetSpeedLimitNegative(t *testing.T) {
	resp, be, _ := toolResult(t, "set_speed_limit", map[string]any{"adapter": "Wi-Fi", "mbps": -3})
	if _, ok := resp["error"]; !ok {
		t.Errorf("negative mbps: response %v has no error key", resp)
	}
	if got := be.limitCalls(); len(got) != 0 {
		t.Errorf("negative mbps reached the backend: %+v", got)
	}
}

// ---------------------------------------------------------------------------------------------
// 5. Fallback and cooldowns

// failFirst makes model "a" fail once with fail, then answer "from a"; "b" always answers "from b".
func failFirst(fail func() (int, string)) genHandler {
	return func(model string, n int, req wireRequest) (int, string) {
		if model == "a" {
			if n == 0 {
				return fail()
			}
			return textReply("from a")
		}
		return textReply("from " + model)
	}
}

func TestSpecFallback503Cooldown(t *testing.T) {
	s, api, _, clk := newSpecSession(t, []string{"a", "b"}, failFirst(overloaded))
	ctx := specCtx(t)

	reply, err := s.Send(ctx, "hello")
	if err != nil {
		t.Fatalf("Send 1: %v", err)
	}
	if reply.Model != "b" || reply.Text != "from b" {
		t.Errorf("Send 1 = %+v, want answered by b", reply)
	}
	if n := api.genCount("a"); n != 1 {
		t.Fatalf("a got %d requests on Send 1, want 1", n)
	}

	clk.Advance(29 * time.Second)
	reply, err = s.Send(ctx, "hello again")
	if err != nil {
		t.Fatalf("Send 2: %v", err)
	}
	if reply.Model != "b" {
		t.Errorf("Send 2 Model = %q, want b", reply.Model)
	}
	if n := api.genCount("a"); n != 1 {
		t.Errorf("a was requested again within its 30s cooldown (%d requests)", n)
	}

	clk.Advance(2 * time.Second) // 31s after the failure
	reply, err = s.Send(ctx, "and again")
	if err != nil {
		t.Fatalf("Send 3: %v", err)
	}
	if n := api.genCount("a"); n != 2 {
		t.Errorf("a got %d requests after its cooldown, want 2", n)
	}
	if reply.Model != "a" {
		t.Errorf("Send 3 Model = %q, want a (cooled down)", reply.Model)
	}
}

func TestSpecFallback404(t *testing.T) {
	s, api, _, _ := newSpecSession(t, []string{"a", "b"}, func(model string, n int, req wireRequest) (int, string) {
		if model == "a" {
			return notFound(model)
		}
		return textReply("from b")
	})
	reply, err := s.Send(specCtx(t), "hello")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if reply.Model != "b" {
		t.Errorf("Model = %q, want b", reply.Model)
	}
	if api.genCount("a") != 1 || api.genCount("b") != 1 {
		t.Errorf("requests a=%d b=%d, want 1 each", api.genCount("a"), api.genCount("b"))
	}
}

func TestSpecFallback429RetryDelay(t *testing.T) {
	s, api, _, clk := newSpecSession(t, []string{"a", "b"}, failFirst(func() (int, string) {
		return http.StatusTooManyRequests, errBody(429, "RESOURCE_EXHAUSTED", "You exceeded your current quota.", retryInfo("7s"))
	}))
	ctx := specCtx(t)

	reply, err := s.Send(ctx, "hello")
	if err != nil {
		t.Fatalf("Send 1: %v", err)
	}
	if reply.Model != "b" {
		t.Errorf("Send 1 Model = %q, want b", reply.Model)
	}

	clk.Advance(6 * time.Second)
	reply, err = s.Send(ctx, "hello again")
	if err != nil {
		t.Fatalf("Send 2: %v", err)
	}
	if reply.Model != "b" || api.genCount("a") != 1 {
		t.Errorf("6s after a 7s retryDelay: Model=%q, a requests=%d; want b and 1", reply.Model, api.genCount("a"))
	}

	clk.Advance(1500 * time.Millisecond) // 7.5s after the failure
	reply, err = s.Send(ctx, "and again")
	if err != nil {
		t.Fatalf("Send 3: %v", err)
	}
	if reply.Model != "a" || api.genCount("a") != 2 {
		t.Errorf("7.5s after a 7s retryDelay: Model=%q, a requests=%d; want a and 2", reply.Model, api.genCount("a"))
	}
}

// ---------------------------------------------------------------------------------------------
// 6. Mid-turn failure restarts the whole message on the next model

func TestSpecMidTurnFailureRestarts(t *testing.T) {
	s, api, be, _ := newSpecSession(t, []string{"a", "b"}, func(model string, n int, req wireRequest) (int, string) {
		if model == "a" {
			if n == 0 {
				return http.StatusOK, respParts(pCall("c1", "set_app_lane", map[string]any{"app": "Spotify.exe", "lane": "merge"}, "SIG_A"))
			}
			return overloaded()
		}
		return textReply("Spotify is merged.")
	})
	reply, err := s.Send(specCtx(t), "merge spotify")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if reply.Model != "b" {
		t.Errorf("Model = %q, want b", reply.Model)
	}
	if got := be.laneCalls(); len(got) == 0 || got[0] != (laneCall{"Spotify.exe", apps.Mode{Kind: apps.ModeMerge}}) {
		t.Errorf("SetAppLane calls = %+v, want a's call for Spotify.exe merge executed", got)
	}
	if n := api.genCount("a"); n != 2 {
		t.Errorf("a got %d requests, want 2 (call, then failing follow-up)", n)
	}
	bc := api.genCalls("b")
	if len(bc) == 0 {
		t.Fatal("b was never asked")
	}
	req := bc[0].Req
	if len(req.Contents) != 1 {
		t.Errorf("b's first request has %d contents, want only the user text: %s", len(req.Contents), req.Contents)
	}
	for i := range req.Contents {
		c := req.mustContent(t, i)
		if hasFunctionParts(c) {
			t.Errorf("b's first request carries function history from a in contents[%d]", i)
		}
	}
	if c := req.mustContent(t, -1); c.Role != "user" || plainText(c) != "merge spotify" {
		t.Errorf("b's first request does not end with the user text: %+v", c)
	}
}

// ---------------------------------------------------------------------------------------------
// 7. Bad key stops at once

func TestSpecBadKey(t *testing.T) {
	cases := map[string]func() (int, string){
		"400 API_KEY_INVALID": func() (int, string) {
			return http.StatusBadRequest, errBody(400, "INVALID_ARGUMENT", "Request rejected.", errorInfo("API_KEY_INVALID"))
		},
		"403": func() (int, string) {
			return http.StatusForbidden, errBody(403, "PERMISSION_DENIED", "Method doesn't allow unregistered callers.")
		},
	}
	for name, fail := range cases {
		t.Run(name, func(t *testing.T) {
			s, api, _, _ := newSpecSession(t, []string{"a", "b"}, func(model string, n int, req wireRequest) (int, string) {
				if model == "a" {
					return fail()
				}
				return textReply("from b")
			})
			_, err := s.Send(specCtx(t), "hello")
			if !errors.Is(err, ErrBadKey) {
				t.Errorf("err = %v, want ErrBadKey", err)
			}
			if n := api.genCount("b"); n != 0 {
				t.Errorf("b was tried %d times after a bad key, want 0", n)
			}
		})
	}
}

// ---------------------------------------------------------------------------------------------
// 8. All models failing

func TestSpecAllFailing(t *testing.T) {
	s, api, _, _ := newSpecSession(t, []string{"a", "b"}, func(model string, n int, req wireRequest) (int, string) {
		if model == "a" {
			return overloaded()
		}
		return http.StatusInternalServerError, errBody(500, "INTERNAL", "An internal error has occurred.")
	})
	_, err := s.Send(specCtx(t), "hello")
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want it to wrap ErrUnavailable", err)
	}
	if errors.Is(err, ErrBadKey) {
		t.Errorf("err = %v must not be ErrBadKey", err)
	}
	if api.genCount("a") != 1 || api.genCount("b") != 1 {
		t.Errorf("requests a=%d b=%d, want 1 each", api.genCount("a"), api.genCount("b"))
	}
	// Both are cooling down now: still ErrUnavailable.
	_, err = s.Send(specCtx(t), "hello again")
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("second Send err = %v, want it to wrap ErrUnavailable", err)
	}
}

// ---------------------------------------------------------------------------------------------
// 9. History

// historyHandler answers "mN" with "rN"; for "m2" it first calls get_status, so message 2 has
// function parts that must not leak into later requests.
func historyHandler(model string, n int, req wireRequest) (int, string) {
	last, err := req.content(-1)
	if err != nil {
		return http.StatusBadRequest, errBody(400, "INVALID_ARGUMENT", "no contents")
	}
	if hasFunctionParts(last) {
		return textReply("r2")
	}
	txt := plainText(last)
	if txt == "m2" {
		return http.StatusOK, respParts(pCall("g1", "get_status", nil, "SIG_H"))
	}
	return textReply("r" + strings.TrimPrefix(txt, "m"))
}

type turn struct{ Role, Text string }

// firstRequestFor returns the turns of the first request whose last content is the user text msg,
// failing on any function part.
func firstRequestFor(t *testing.T, api *fakeAPI, msg string) []turn {
	t.Helper()
	for _, c := range api.genCalls("a") {
		last, err := c.Req.content(-1)
		if err != nil || last.Role != "user" || plainText(last) != msg {
			continue
		}
		var out []turn
		for i := range c.Req.Contents {
			ct := c.Req.mustContent(t, i)
			if hasFunctionParts(ct) {
				t.Errorf("request for %q: contents[%d] (%s) carries function parts from an earlier message", msg, i, c.Req.Contents[i])
			}
			out = append(out, turn{ct.Role, plainText(ct)})
		}
		return out
	}
	t.Fatalf("no request ended with user text %q", msg)
	return nil
}

func sendAll(t *testing.T, s *Session, msgs ...string) {
	t.Helper()
	for _, m := range msgs {
		reply, err := s.Send(specCtx(t), m)
		if err != nil {
			t.Fatalf("Send(%q): %v", m, err)
		}
		if want := "r" + strings.TrimPrefix(m, "m"); reply.Text != want {
			t.Fatalf("Send(%q).Text = %q, want %q", m, reply.Text, want)
		}
	}
}

func TestSpecHistoryTextOnly(t *testing.T) {
	s, api, _, _ := newSpecSession(t, []string{"a"}, historyHandler)
	sendAll(t, s, "m1", "m2", "m3")
	got := firstRequestFor(t, api, "m3")
	want := []turn{{"user", "m1"}, {"model", "r1"}, {"user", "m2"}, {"model", "r2"}, {"user", "m3"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("third request contents = %+v, want %+v", got, want)
	}
}

func TestSpecHistoryMaxHistory(t *testing.T) {
	s, api, _, _ := newSpecSession(t, []string{"a"}, historyHandler)
	s.MaxHistory = 2
	sendAll(t, s, "m1", "m2", "m3")
	got := firstRequestFor(t, api, "m3")
	want := []turn{{"user", "m2"}, {"model", "r2"}, {"user", "m3"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("with MaxHistory=2 third request contents = %+v, want %+v", got, want)
	}
}

func TestSpecHistoryReset(t *testing.T) {
	s, api, _, _ := newSpecSession(t, []string{"a"}, historyHandler)
	sendAll(t, s, "m1", "m2")
	s.Reset()
	sendAll(t, s, "m3")
	got := firstRequestFor(t, api, "m3")
	want := []turn{{"user", "m3"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("after Reset request contents = %+v, want %+v", got, want)
	}
}

// ---------------------------------------------------------------------------------------------
// 10. System instruction carries the snapshot

func TestSpecSystemInstructionHasSnapshot(t *testing.T) {
	s, api, _, _ := newSpecSession(t, []string{"a"}, func(model string, n int, req wireRequest) (int, string) {
		return textReply("ok")
	})
	if _, err := s.Send(specCtx(t), "status?"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	calls := api.genCalls("a")
	if len(calls) != 1 {
		t.Fatalf("got %d requests, want 1", len(calls))
	}
	si := calls[0].Req.SystemInstruction
	if si == nil {
		t.Fatal("request has no systemInstruction")
	}
	var sb strings.Builder
	for _, p := range si.Parts {
		sb.WriteString(p.Text)
	}
	for _, want := range []string{"Spotify.exe", "brave.exe", "Wi-Fi", "Ethernet"} {
		if !strings.Contains(sb.String(), want) {
			t.Errorf("systemInstruction lacks %q", want)
		}
	}
	if len(calls[0].Req.Tools) == 0 {
		t.Errorf("request declares no tools")
	}
}

// ---------------------------------------------------------------------------------------------
// 11. Discovery when every configured model is gone

const discoveryList = `{"models":[
 {"name":"models/gemini-9-flash-image","supportedGenerationMethods":["generateContent"]},
 {"name":"models/gemini-9-flash","supportedGenerationMethods":["generateContent","countTokens"]},
 {"name":"models/gemini-9-flash-tts","supportedGenerationMethods":["generateContent"]},
 {"name":"models/gemini-9-flash-lite-preview-09-2026","supportedGenerationMethods":["generateContent"]},
 {"name":"models/gemini-9-pro","supportedGenerationMethods":["generateContent"]},
 {"name":"models/gemini-9-flash-lite","supportedGenerationMethods":["generateContent","countTokens"]},
 {"name":"models/text-embedding-9","supportedGenerationMethods":["embedContent"]}
]}`

func TestSpecDiscoverySucceeds(t *testing.T) {
	s, api, _, _ := newSpecSession(t, []string{"gone1", "gone2"}, func(model string, n int, req wireRequest) (int, string) {
		if strings.HasPrefix(model, "gone") {
			return notFound(model)
		}
		return textReply("found " + model)
	})
	api.list = discoveryList
	reply, err := s.Send(specCtx(t), "hello")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if reply.Model != "gemini-9-flash-lite" {
		t.Errorf("Model = %q, want gemini-9-flash-lite (lite preferred)", reply.Model)
	}
	if n := api.listCount(); n != 1 {
		t.Errorf("GET /models called %d times, want 1", n)
	}
	var order []string
	for _, c := range api.all() {
		if c.Model != "" {
			order = append(order, c.Model)
		} else {
			order = append(order, c.Method+" "+c.Path)
		}
	}
	want := []string{"gone1", "gone2", "GET /models", "gemini-9-flash-lite"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("request order = %v, want %v", order, want)
	}
}

func TestSpecDiscoveryFiltersAndOrders(t *testing.T) {
	s, api, _, _ := newSpecSession(t, []string{"gone1", "gone2"}, func(model string, n int, req wireRequest) (int, string) {
		if strings.HasPrefix(model, "gone") {
			return notFound(model)
		}
		return overloaded()
	})
	api.list = discoveryList
	_, err := s.Send(specCtx(t), "hello")
	if !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want it to wrap ErrUnavailable", err)
	}
	if n := api.listCount(); n != 1 {
		t.Errorf("GET /models called %d times, want 1", n)
	}
	var tried []string
	for _, c := range api.all() {
		if c.Model != "" && !strings.HasPrefix(c.Model, "gone") {
			tried = append(tried, c.Model)
		}
	}
	want := []string{"gemini-9-flash-lite", "gemini-9-flash"}
	if !reflect.DeepEqual(tried, want) {
		t.Errorf("discovered models tried = %v, want %v (flash only, no image/tts/preview, lite first)", tried, want)
	}
}

// ---------------------------------------------------------------------------------------------
// 12. Empty answer falls back

func TestSpecEmptyAnswerFallsBack(t *testing.T) {
	cases := map[string]string{
		"no parts":   respWithContent(`{"role":"model","parts":[]}`),
		"no content": `{"candidates":[{"finishReason":"STOP"}]}`,
	}
	for name, empty := range cases {
		t.Run(name, func(t *testing.T) {
			s, api, _, _ := newSpecSession(t, []string{"a", "b"}, func(model string, n int, req wireRequest) (int, string) {
				if model == "a" {
					return http.StatusOK, empty
				}
				return textReply("from b")
			})
			reply, err := s.Send(specCtx(t), "hello")
			if err != nil {
				t.Fatalf("Send: %v", err)
			}
			if reply.Model != "b" || reply.Text != "from b" {
				t.Errorf("reply = %+v, want answered by b", reply)
			}
			if n := api.genCount("a"); n != 1 {
				t.Errorf("a got %d requests, want 1", n)
			}
		})
	}
}

// ---------------------------------------------------------------------------------------------
// 13. MaxRounds

func TestSpecMaxRounds(t *testing.T) {
	s, api, _, _ := newSpecSession(t, []string{"a", "b"}, func(model string, n int, req wireRequest) (int, string) {
		return http.StatusOK, respParts(pCall("g", "get_status", nil, "SIG_LOOP"))
	})
	s.MaxRounds = 3
	reply, err := s.Send(specCtx(t), "loop forever")
	if err != nil {
		t.Fatalf("Send: %v, want a Reply without error", err)
	}
	if n := api.genCount("a"); n != 3 {
		t.Errorf("a got %d requests, want MaxRounds=3", n)
	}
	if n := api.genCount("b"); n != 0 {
		t.Errorf("b got %d requests; hitting MaxRounds is not a model failure", n)
	}
	if reply.Model != "a" {
		t.Errorf("Model = %q, want a", reply.Model)
	}
}

// ---------------------------------------------------------------------------------------------
// 14. The key travels in a header, never in the URL

func TestSpecKeyInHeaderOnly(t *testing.T) {
	s, api, _, _ := newSpecSession(t, []string{"gone"}, func(model string, n int, req wireRequest) (int, string) {
		if model == "gone" {
			return notFound(model)
		}
		return textReply("ok")
	})
	api.list = discoveryList
	if _, err := s.Send(specCtx(t), "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	calls := api.all()
	sawGet, sawPost := false, false
	for _, c := range calls {
		if c.Method == http.MethodGet {
			sawGet = true
		}
		if c.Method == http.MethodPost {
			sawPost = true
		}
		if c.Key != specKey {
			t.Errorf("%s %s: x-goog-api-key = %q, want %q", c.Method, c.Path, c.Key, specKey)
		}
		if strings.Contains(c.URI, specKey) || strings.Contains(c.URI, "key=") {
			t.Errorf("%s: key in URL %q", c.Method, c.URI)
		}
		if strings.Contains(string(c.Raw), specKey) {
			t.Errorf("%s %s: key in request body", c.Method, c.Path)
		}
	}
	if !sawGet || !sawPost {
		t.Errorf("want both a generateContent and a model-list request, got %d requests", len(calls))
	}
}
