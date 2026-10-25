package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ollamaCall mirrors the /api/generate payload ollamaChat actually sends (raw ChatML prompt,
// not the /api/chat "messages" shape).
type ollamaCall struct {
	Model     string   `json:"model"`
	Prompt    string   `json:"prompt"`
	KeepAlive any      `json:"keep_alive"`
	Images    []string `json:"images,omitempty"`
}

// gateway spins up a fake web gateway; reply decides what /api/vision/analyze answers
func gateway(t *testing.T, reply func(w http.ResponseWriter, body map[string]string)) (calls *atomic.Int32) {
	t.Helper()
	calls = &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/vision/analyze" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		reply(w, body)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("SENTCODE_GATEWAY", srv.URL)
	return calls
}

func okSpec(spec string) func(http.ResponseWriter, map[string]string) {
	return func(w http.ResponseWriter, _ map[string]string) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "spec": spec})
	}
}

// coderEngine fakes Ollama and records every chat call
func coderEngine(t *testing.T, a *App) *[]ollamaCall {
	t.Helper()
	var calls []ollamaCall
	fakeEngine(t, a, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var c ollamaCall
		_ = json.NewDecoder(r.Body).Decode(&c)
		calls = append(calls, c)
		if strings.Contains(c.Model, "vl") {
			fmt.Fprint(w, `{"response":"LOCAL-EYES: a settings panel"}`)
		} else {
			fmt.Fprint(w, `{"response":"here is the code"}`)
		}
	}))
	coder, _ := a.findModel("qwen-coder-7b")
	installFakeWeights(coder.OllamaTag)
	return &calls
}

const testImage = "data:image/png;base64,QUJD"

func hasViewAction(resp ChatResponse, via string) bool {
	for _, act := range resp.Actions {
		if act.Type == "view_image" && strings.Contains(act.Detail, via) {
			return true
		}
	}
	return false
}

func TestVisionViaGateway(t *testing.T) {
	a := isolate(t)
	engineCalls := coderEngine(t, a)
	var gotPrompt string
	gw := gateway(t, func(w http.ResponseWriter, body map[string]string) {
		gotPrompt = body["prompt"]
		okSpec("LAYOUT: two cards. BUTTON: Refill 100 Tokens (#00b8d9)")(w, body)
	})
	coder, _ := a.findModel("qwen-coder-7b")

	resp := a.runRegistryModel(ChatRequest{Message: "match this", Images: []string{testImage}}, coder, "")
	if resp.Status != "ok" || resp.Reply != "here is the code" {
		t.Fatalf("resp: %+v", resp)
	}
	if gw.Load() != 1 || len(*engineCalls) != 1 {
		t.Fatalf("want 1 gateway call + 1 coder call, got %d + %d", gw.Load(), len(*engineCalls))
	}
	if !strings.Contains(gotPrompt, "match this") {
		t.Errorf("the user's request must reach the vision prompt: %q", gotPrompt)
	}
	c := (*engineCalls)[0]
	if c.Model != "qwen2.5-coder:7b" || len(c.Images) != 0 {
		t.Errorf("coder must not receive images: %+v", c)
	}
	if !strings.Contains(c.Prompt, "match this") || !strings.Contains(c.Prompt, "Refill 100 Tokens (#00b8d9)") {
		t.Errorf("coder prompt must contain the user's words and the spec: %q", c.Prompt)
	}
	if !hasViewAction(resp, "Vision Gateway") {
		t.Errorf("chat should show the image was read via the gateway: %+v", resp.Actions)
	}
}

func TestVisionGatewayFakeAnswersAreNotTrusted(t *testing.T) {
	for name, spec := range map[string]string{
		"placeholder (no Groq key)": gatewayPlaceholderSpec + "\n- Detected visual elements: Modern user interface",
		"parse failure":             gatewayParseFailure,
		"empty":                     "  ",
		// verified live against the real running gateway: it answers success:true with exactly
		// this text when the underlying Groq vision model id has gone stale.
		"real gateway failure": "Unable to analyze image.",
		"apologetic variant":   "I'm sorry, I cannot analyze this image right now.",
	} {
		t.Run(name, func(t *testing.T) {
			a := isolate(t)
			engineCalls := coderEngine(t, a)
			gateway(t, okSpec(spec))
			coder, _ := a.findModel("qwen-coder-7b")

			resp := a.runRegistryModel(ChatRequest{Message: "match this", Images: []string{testImage}}, coder, "")
			if resp.Status != "ok" || !strings.Contains(resp.Reply, "ยังไม่พร้อมทำงาน") {
				t.Fatalf("user must be told the image was not analyzed: %+v", resp)
			}
			prompt := (*engineCalls)[0].Prompt
			if strings.Contains(prompt, "Modern user interface") || strings.Contains(prompt, gatewayParseFailure) {
				t.Errorf("a fake spec leaked into the coder prompt")
			}
			if hasViewAction(resp, "") {
				t.Errorf("must not claim the image was viewed")
			}
		})
	}
}

func TestVisionGatewayDownFallsBackToLocalEyesOrDegrades(t *testing.T) {
	a := isolate(t)
	engineCalls := coderEngine(t, a)
	t.Setenv("SENTCODE_GATEWAY", "http://127.0.0.1:1") // nothing listens here
	coder, _ := a.findModel("qwen-coder-7b")

	// no local eyes: text-only with a note, not a failure, no token refund needed
	resp := a.runRegistryModel(ChatRequest{Message: "match this", Images: []string{testImage}}, coder, "")
	if resp.Status != "ok" || !strings.Contains(resp.Reply, "ยังไม่พร้อมทำงาน") || len(*engineCalls) != 1 {
		t.Fatalf("degrade: status=%s calls=%d reply=%q", resp.Status, len(*engineCalls), resp.Reply)
	}

	// user-added local vision model becomes the offline eyes
	*engineCalls = nil
	if _, err := a.AddModel(ModelDef{Kind: KindOllama, Name: "My VL", OllamaTag: "qwen2.5vl:3b", SupportsVision: true}); err != nil {
		t.Fatal(err)
	}
	installFakeWeights("qwen2.5vl:3b")
	resp = a.runRegistryModel(ChatRequest{Message: "match this", Images: []string{testImage}}, coder, "")
	if resp.Status != "ok" || len(*engineCalls) != 2 {
		t.Fatalf("fallback: status=%s calls=%d", resp.Status, len(*engineCalls))
	}
	eyes, brain := (*engineCalls)[0], (*engineCalls)[1]
	if len(eyes.Images) != 1 || eyes.Images[0] != "QUJD" || eyes.KeepAlive != float64(0) {
		t.Errorf("local eyes must get the bare base64 image and be unloaded after use: %+v", eyes)
	}
	if !strings.Contains(brain.Prompt, "LOCAL-EYES") || !hasViewAction(resp, "My VL") {
		t.Errorf("coder must receive the local spec: %q / %+v", brain.Prompt, resp.Actions)
	}
}

func TestVisionSkippedWhenNotNeeded(t *testing.T) {
	a := isolate(t)
	engineCalls := coderEngine(t, a)
	gw := gateway(t, okSpec("spec"))
	coder, _ := a.findModel("qwen-coder-7b")

	a.runRegistryModel(ChatRequest{Message: "plain question"}, coder, "")
	if gw.Load() != 0 || len(*engineCalls) != 1 {
		t.Errorf("text-only must never touch the gateway: gw=%d", gw.Load())
	}

	a.isOnline = false
	resp := a.runRegistryModel(ChatRequest{Message: "match this", Images: []string{testImage}}, coder, "")
	if gw.Load() != 0 || !strings.Contains(resp.Reply, "ยังไม่พร้อมทำงาน") {
		t.Errorf("offline must not call the gateway: gw=%d reply=%q", gw.Load(), resp.Reply)
	}
}

func TestVisionSendsMultipleImagesNumbered(t *testing.T) {
	a := isolate(t)
	engineCalls := coderEngine(t, a)
	n := 0
	gateway(t, func(w http.ResponseWriter, body map[string]string) {
		n++
		okSpec(fmt.Sprintf("spec-%d", n))(w, body)
	})
	coder, _ := a.findModel("qwen-coder-7b")
	imgs := []string{testImage, testImage, testImage, testImage, testImage, testImage}
	a.runRegistryModel(ChatRequest{Message: "compare", Images: imgs}, coder, "")
	prompt := (*engineCalls)[0].Prompt
	if n != maxVisionImages || !strings.Contains(prompt, "Image 1:\nspec-1") || !strings.Contains(prompt, "Image 4:\nspec-4") {
		t.Errorf("images must be capped at %d and numbered: n=%d %q", maxVisionImages, n, prompt)
	}
}

func TestPrepareImageForVision(t *testing.T) {
	encode := func(w, h int, noisy bool) string {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		rng := rand.New(rand.NewSource(1))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				c := color.RGBA{30, 40, 60, 255}
				if noisy {
					c = color.RGBA{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), 255}
				}
				img.SetRGBA(x, y, c)
			}
		}
		var buf bytes.Buffer
		_ = png.Encode(&buf, img)
		return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	}
	decode := func(uri string) (image.Config, string) {
		raw, _ := base64.StdEncoding.DecodeString(uri[strings.Index(uri, ",")+1:])
		cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		return cfg, format
	}

	small := encode(300, 200, false)
	if prepareImageForVision(small) != small {
		t.Error("small images must pass through untouched")
	}

	big := encode(3200, 2000, true) // > 3 MB of noise and larger than the edge limit
	out := prepareImageForVision(big)
	cfg, format := decode(out)
	if format != "jpeg" || max(cfg.Width, cfg.Height) != visionMaxEdge || cfg.Width != 1600 || cfg.Height != 1000 {
		t.Errorf("want 1600x1000 jpeg, got %dx%d %s", cfg.Width, cfg.Height, format)
	}
	if len(out) > 4<<20 {
		t.Errorf("result still too big for Groq: %d bytes", len(out))
	}

	// undecodable input is forwarded as-is rather than dropped
	if got := prepareImageForVision("data:image/png;base64,QUJD"); got != "data:image/png;base64,QUJD" {
		t.Errorf("undecodable image must pass through, got %q", got)
	}
}

func TestPullStallIsDetectedAndRetried(t *testing.T) {
	oldIdle, oldDelay := pullIdleTimeout, pullRetryDelay
	pullIdleTimeout, pullRetryDelay = 300*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { pullIdleTimeout, pullRetryDelay = oldIdle, oldDelay })

	a := isolate(t)
	var attempts atomic.Int32
	fakeEngine(t, a, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		if attempts.Add(1) == 1 {
			// first attempt: some progress, then the connection goes silent forever
			fmt.Fprintln(w, `{"status":"pulling aaa","digest":"aaa","total":100,"completed":40}`)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		fmt.Fprintln(w, `{"status":"pulling aaa","digest":"aaa","total":100,"completed":100}`)
		fmt.Fprintln(w, `{"status":"success"}`)
	}))

	start := time.Now()
	err := a.pullModel(context.Background(), ModelDef{ID: "x", OllamaTag: "ok:1"})
	if err != nil {
		t.Fatalf("stalled download must recover on retry, got %v", err)
	}
	if attempts.Load() != 2 || time.Since(start) > 5*time.Second {
		t.Errorf("attempts=%d after %s", attempts.Load(), time.Since(start))
	}
}

func TestPullGivesUpAfterRepeatedStallsButNotOnUserCancel(t *testing.T) {
	oldIdle, oldDelay := pullIdleTimeout, pullRetryDelay
	pullIdleTimeout, pullRetryDelay = 100*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { pullIdleTimeout, pullRetryDelay = oldIdle, oldDelay })

	a := isolate(t)
	var attempts atomic.Int32
	fakeEngine(t, a, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.(http.Flusher).Flush()
		<-r.Context().Done() // never sends anything
	}))

	err := a.pullModel(context.Background(), ModelDef{ID: "x", OllamaTag: "ok:1"})
	if err == nil || attempts.Load() != pullAttempts {
		t.Fatalf("want failure after %d attempts, got err=%v attempts=%d", pullAttempts, err, attempts.Load())
	}

	// user pressing Pause must stop immediately, not burn through retries
	attempts.Store(0)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	err = a.pullModel(ctx, ModelDef{ID: "x", OllamaTag: "ok:1"})
	if err == nil || attempts.Load() != 1 {
		t.Errorf("cancel must not retry: err=%v attempts=%d", err, attempts.Load())
	}
}

func TestLooksLikeGatewayFailure(t *testing.T) {
	failures := []string{
		"", "  ",
		"Unable to analyze image.", // verified live, real gateway response
		"Unable to analyze image",
		"I'm sorry, I cannot analyze this image right now.",
		"Sorry, I couldn't analyze that.",
		"Error analyzing image: timeout",
		"Unable to parse vision spec",
	}
	for _, s := range failures {
		if !looksLikeGatewayFailure(s) {
			t.Errorf("must be treated as a failure: %q", s)
		}
	}

	// A real spec is long and structured; it must never be rejected just because it happens to
	// use a word like "error" while genuinely describing what's in the image.
	real := "1. What the image is: a screenshot of a terminal showing a build error.\n" +
		"2. Layout: a dark terminal window filling the frame.\n" +
		"3. Components: none, just text output.\n" +
		"4. Style: monospace white-on-black.\n" +
		"5. Error text (verbatim): \"panic: runtime error: index out of range\" at main.go:42.\n" +
		"6. This looks like a Go panic the user needs fixed."
	if looksLikeGatewayFailure(real) {
		t.Errorf("a real, detailed spec must not be rejected: %q", real)
	}
}
