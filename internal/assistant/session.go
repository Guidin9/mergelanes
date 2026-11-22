package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
)

const systemPrompt = `You are the assistant inside MergeLanes, an app that merges several internet connections ("adapters", for example Ethernet and Wi-Fi) into one, and lets each app use its own lane.

Lanes an app can have:
- auto: the default. Games and voice chat go to the adapter with the lowest ping; everything else is merged.
- merge: every connection of the app is spread over all adapters (best for downloads).
- fastest: every connection on the adapter with the lowest ping.
- adapter: every connection only on one named adapter; if that adapter fails, the fastest other one.
A lane change applies to the app's new connections. Open connections stay where they are until the app reconnects, so say that when it matters.

How to work:
- Change settings only with the tools, and never say something changed unless a tool result confirms it. If a tool returns an error, explain it briefly or ask.
- App names are exe names from the state below. Map what the user says ("spotify", "brave", "epic", "discord") to them, but only when the exe name clearly belongs to that product (Epic's start with "Epic"); never guess from a generic word such as "download". If one product has several exes (Epic has a launcher and helpers), change all of them and say so.
- The apps list holds apps that opened a connection in the last 10 minutes, plus apps with a saved rule. A long download that opened its connections earlier may be missing: then say the app has not opened a new connection recently, rather than naming another app. For a change to an app that is not listed, use its exe name if the user gives it or it is well known (EpicOnlineServicesInstallHelper.exe downloads Epic games), otherwise ask.
- Adapter names come from the state below; "wifi", "kablosuz", "wireless" mean the Wi-Fi adapter; "kablo", "kablolu", "cable" mean Ethernet.
- To answer which connection an app uses, read its rule and connections_by_adapter: for a merged app, say how its connections are split.
- Speed limits are in Mbit/s; 0 removes the limit. Turning merging off sends everything over the system's normal connection.
- Reply in the user's language, in one to three short sentences of plain text, no markdown, no lists. Name lanes in that language too; in Turkish they are Otomatik, Birleştir, En düşük ping and Sadece <adapter>.

When the user asks which lane to use ("which mode should Discord use?", "what's best for Steam?"), recommend one and say why in a sentence, using the live pings and speeds when they matter. Do not change anything for a question like that: end by offering to set it, and set it only when they say so. Guidance:
- Voice and video calls, and games (Discord, Teams, Zoom, game exes): auto. It keeps calls and game traffic on the lowest-ping adapter and still merges their downloads and updates. Suggest fastest only if they want every connection of the app on one adapter, or adapter when one link is clearly steadier (much lower ping) and they want to lock the app to it.
- Game launchers, downloaders and torrents (Steam, Epic, Battle.net, qBittorrent, download managers): merge, since downloads that open several connections then use both links. Mention that a download over a single connection cannot be split.
- Browsers and music or video streaming (Brave, Chrome, Spotify, Netflix): auto is right for most use. If a website keeps logging them out or a bank complains, pin the browser to one adapter, because merged connections can reach a site from different addresses.
- If one adapter is down or very slow right now, say so and suggest the other.

Current state, refreshed at every message (numbers are Mbit/s and ms):
`

// Reply is the answer to one message.
type Reply struct {
	Text    string   `json:"text"`
	Actions []Action `json:"actions,omitempty"`
	Model   string   `json:"model"`
}

// Session is one conversation. Send is safe to call from several goroutines; calls run one at a time.
type Session struct {
	G *Gemini
	B Backend

	MaxRounds  int // tool rounds per message; 0 = 6
	MaxHistory int // past messages kept (user and model each count); 0 = 16

	mu      sync.Mutex
	history []json.RawMessage // text only: tool calls stay inside the message that made them
}

// Reset forgets the conversation.
func (s *Session) Reset() {
	s.mu.Lock()
	s.history = nil
	s.mu.Unlock()
}

func textContent(role, text string) json.RawMessage {
	return mustRaw(content{Role: role, Parts: []part{{Text: text}}})
}

// Send answers one message, running tools as the model asks. If a model fails partway, the whole
// message starts over on the next model: the tools only set absolute values, so repeating one is
// harmless.
func (s *Session) Send(ctx context.Context, text string) (Reply, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Reply{}, errors.New("empty message")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	rounds := s.MaxRounds
	if rounds <= 0 {
		rounds = 6
	}
	models := s.G.ready()
	if len(models) == 0 {
		return Reply{}, ErrUnavailable
	}
	// The state is read once per message, not per model: a failure here is not the model's.
	snap, err := s.B.Snapshot()
	if err != nil {
		return Reply{}, err
	}
	state, _ := json.Marshal(snapshotView(snap))
	sys := &content{Parts: []part{{Text: systemPrompt + string(state)}}}

	var lastErr error = ErrUnavailable
	var carried []Action // changes made by attempts that then failed: they did happen
	discovered := false
	for i := 0; i < len(models); i++ {
		model := models[i]
		reply, err := s.turn(ctx, model, sys, text, rounds)
		if err == nil {
			reply.Actions = mergeActions(carried, reply.Actions)
			s.remember(text, reply.Text)
			return reply, nil
		}
		carried = mergeActions(carried, reply.Actions)
		if ctx.Err() != nil {
			return Reply{Actions: carried}, ctx.Err()
		}
		d := cooling(err)
		if d == 0 {
			return Reply{Actions: carried}, ErrBadKey
		}
		s.G.coolDown(model, d)
		lastErr = err
		if i == len(models)-1 && !discovered && allMissing(err) {
			// Every model we know is gone: look up current ones once and keep going.
			discovered = true
			if s.G.Discover(ctx) == nil {
				for _, m := range s.G.ready() {
					if !contains(models, m) {
						models = append(models, m)
					}
				}
			}
		}
	}
	return Reply{Actions: carried}, errors.Join(ErrUnavailable, lastErr)
}

// mergeActions appends later to earlier, dropping a later action that repeats an earlier one (the
// same change made again by a retry), and keeping a success over a failure of the same change.
func mergeActions(earlier, later []Action) []Action {
	out := append([]Action(nil), earlier...)
	for _, a := range later {
		dup := false
		for i := range out {
			if out[i].Tool == a.Tool && out[i].Summary == a.Summary {
				out[i].OK = out[i].OK || a.OK
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, a)
		}
	}
	return out
}

func allMissing(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.Status == 404
}

func (s *Session) remember(user, model string) {
	s.history = append(s.history, textContent("user", user), textContent("model", model))
	max := s.MaxHistory
	if max <= 0 {
		max = 16
	}
	if n := len(s.history); n > max {
		s.history = append([]json.RawMessage(nil), s.history[n-max:]...)
	}
}

// errEmpty is a model turn with neither text nor a tool call (or a malformed call): another model
// may do better.
var errEmpty = &apiError{Status: 502, Message: "the model gave an empty or malformed answer"}

func (s *Session) turn(ctx context.Context, model string, sys *content, text string, rounds int) (Reply, error) {
	contents := append(append([]json.RawMessage(nil), s.history...), textContent("user", text))
	var actions []Action
	for r := 0; r < rounds; r++ {
		resp, err := s.G.generateHedged(ctx, model, &generateRequest{
			SystemInstruction: sys, Contents: contents, Tools: tools,
			GenerationConfig: &generationConfig{MaxOutputTokens: 2048},
		})
		if err != nil {
			return Reply{Actions: actions}, err
		}
		if len(resp.Candidates) == 0 {
			if resp.PromptFeedback != nil && resp.PromptFeedback.BlockReason != "" {
				return Reply{Text: "(Gemini declined to answer: " + resp.PromptFeedback.BlockReason + ")", Actions: actions, Model: model}, nil
			}
			return Reply{Actions: actions}, errEmpty
		}
		cand := resp.Candidates[0]
		var got content
		if len(cand.Content) > 0 {
			if err := json.Unmarshal(cand.Content, &got); err != nil {
				return Reply{Actions: actions}, errEmpty
			}
		}
		var calls []functionCall
		var sb strings.Builder
		for _, p := range got.Parts {
			switch {
			case p.FunctionCall != nil:
				calls = append(calls, *p.FunctionCall)
			case p.Text != "" && !p.Thought:
				sb.WriteString(p.Text)
			}
		}
		if len(calls) == 0 {
			out := strings.TrimSpace(sb.String())
			if out == "" {
				if len(actions) > 0 {
					return Reply{Actions: actions, Model: model}, nil
				}
				return Reply{}, errEmpty
			}
			return Reply{Text: out, Actions: actions, Model: model}, nil
		}
		contents = append(contents, cand.Content) // as received, thought signatures and all
		var results []part
		for _, c := range calls {
			res, act := runTool(s.B, c)
			if act != nil {
				actions = append(actions, *act)
			}
			results = append(results, part{FunctionResponse: &functionResponse{ID: c.ID, Name: c.Name, Response: res}})
		}
		contents = append(contents, mustRaw(content{Role: "user", Parts: results}))
	}
	return Reply{Text: "(stopped after too many steps)", Actions: actions, Model: model}, nil
}
