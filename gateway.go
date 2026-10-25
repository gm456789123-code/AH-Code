package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"strings"
	"time"
)

// defaultGatewayURL is the Sent-Code web gateway.
//
// Override at build time:
//
//	go build -ldflags "-X main.defaultGatewayURL=https://your-domain"
//
// Override at runtime without a rebuild (takes precedence over the compiled value):
//
//	SENTCODE_GATEWAY=https://your-domain ./Sent-Code
//
// In development this defaults to localhost:3000 (the `npm run dev` server).
var defaultGatewayURL = "http://localhost:3000"

func init() {
	if v := strings.TrimRight(os.Getenv("SENTCODE_GATEWAY"), "/"); v != "" {
		defaultGatewayURL = v
	}
}

const (
	maxVisionImages = 4
	// Groq rejects images above ~4 MB of base64, so anything bigger is shrunk first
	visionMaxRawBytes = 3 << 20
	visionMaxEdge     = 1600
)

// The gateway answers with these instead of an error in some situations; neither is a real image analysis
const (
	gatewayPlaceholderSpec = "[Vision Analysis: UI/Layout Context]" // no GROQ_API_KEY configured
	gatewayParseFailure    = "Unable to parse vision spec"          // Groq call failed
)

// gatewayFailurePhrases are the wording the gateway is known to answer with (HTTP 200,
// success:true) when the underlying vision call itself actually failed — e.g. verified live:
// {"success":true,"spec":"Unable to analyze image.","model":"llama-3.2-11b-vision-preview"},
// caused by that model id no longer being valid on Groq's side. None of these are a real spec.
var gatewayFailurePhrases = []string{
	strings.ToLower(gatewayParseFailure),
	"unable to analyze", "cannot analyze", "can't analyze", "couldn't analyze", "could not analyze",
	"failed to analyze", "unable to process the image", "error analyzing", "i'm unable to view",
	"i cannot see", "i don't have the ability to view",
}

// looksLikeGatewayFailure catches short, apologetic non-answers instead of matching one exact
// known phrase: a real spec (see visionSystemPrompt) is a multi-section structured breakdown, so
// anything this short is almost certainly an error message wearing a success:true, not analysis.
func looksLikeGatewayFailure(spec string) bool {
	s := strings.ToLower(strings.TrimSpace(spec))
	if s == "" {
		return true
	}
	if len([]rune(s)) > 150 {
		return false
	}
	for _, p := range gatewayFailurePhrases {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func gatewayURL() string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("SENTCODE_GATEWAY")), "/"); v != "" {
		return v
	}
	return strings.TrimRight(defaultGatewayURL, "/")
}

var gatewayHTTP = &http.Client{Timeout: 4 * time.Second}

// GatewayStatus tells the UI whether the web gateway is reachable
type GatewayStatus struct {
	URL    string `json:"url"`
	Online bool   `json:"online"`
}

// GetGatewayStatus pings the gateway health endpoint
func (a *App) GetGatewayStatus() GatewayStatus {
	st := GatewayStatus{URL: gatewayURL()}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", st.URL+"/api/health", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return st
	}
	defer resp.Body.Close()
	st.Online = resp.StatusCode == 200
	return st
}

// gatewayVision turns the attached images into a technical specification via the web gateway
func (a *App) gatewayVision(ctx context.Context, req ChatRequest) (string, error) {
	imgs := req.Images
	if len(imgs) > maxVisionImages {
		imgs = imgs[:maxVisionImages]
	}
	ask := strings.TrimSpace(req.Message)
	if ask == "" {
		ask = "Describe the image for a developer."
	}
	prompt := visionSystemPrompt + "\n\nThe user's request about this image: " + ask

	var parts []string
	for i, uri := range imgs {
		spec, err := gatewayAnalyze(ctx, prepareImageForVision(uri), prompt)
		if err != nil {
			return "", err
		}
		if len(imgs) > 1 {
			spec = fmt.Sprintf("Image %d:\n%s", i+1, spec)
		}
		parts = append(parts, spec)
	}
	return strings.Join(parts, "\n\n"), nil
}

func gatewayAnalyze(ctx context.Context, dataURI, prompt string) (string, error) {
	body, _ := json.Marshal(map[string]string{"imageBase64": dataURI, "prompt": prompt})
	req, _ := http.NewRequestWithContext(ctx, "POST", gatewayURL()+"/api/vision/analyze", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := gatewayHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out struct {
		Success bool   `json:"success"`
		Spec    string `json:"spec"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("gateway returned unreadable response (%d)", resp.StatusCode)
	}
	if resp.StatusCode != 200 || !out.Success {
		return "", fmt.Errorf("gateway vision failed (%d): %s", resp.StatusCode, out.Error)
	}
	spec := strings.TrimSpace(out.Spec)
	if looksLikeGatewayFailure(spec) || strings.HasPrefix(spec, gatewayPlaceholderSpec) {
		return "", fmt.Errorf("gateway vision is not producing real analysis: %q", spec)
	}
	return spec, nil
}

// prepareImageForVision shrinks oversized screenshots (long edge <= visionMaxEdge, JPEG) so the
// gateway/Groq accept them. Small images and formats we cannot decode pass through untouched.
func prepareImageForVision(dataURI string) string {
	b64 := dataURI
	if i := strings.Index(dataURI, ","); strings.HasPrefix(dataURI, "data:") && i >= 0 {
		b64 = dataURI[i+1:]
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return dataURI
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return dataURI
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if len(raw) <= visionMaxRawBytes && w <= 2000 && h <= 2000 {
		return dataURI
	}

	if long := max(w, h); long > visionMaxEdge {
		img = boxResize(img, w*visionMaxEdge/long, h*visionMaxEdge/long)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		return dataURI
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// boxResize downsamples by averaging the source block behind each destination pixel
func boxResize(src image.Image, nw, nh int) image.Image {
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		y0, y1 := y*sh/nh, (y+1)*sh/nh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < nw; x++ {
			x0, x1 := x*sw/nw, (x+1)*sw/nw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, bl, n uint64
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					cr, cg, cb, _ := src.At(sb.Min.X+xx, sb.Min.Y+yy).RGBA()
					r, g, bl, n = r+uint64(cr), g+uint64(cg), bl+uint64(cb), n+1
				}
			}
			o := dst.PixOffset(x, y)
			dst.Pix[o], dst.Pix[o+1], dst.Pix[o+2], dst.Pix[o+3] = byte(r/n>>8), byte(g/n>>8), byte(bl/n>>8), 255
		}
	}
	return dst
}

// GatewayUser represents an authenticated user on the web gateway
type GatewayUser struct {
	ID           string `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	Avatar       string `json:"avatar"`
	Initials     string `json:"initials"`
	Role         string `json:"role"`
	Tokens       int    `json:"tokens"`
	Plan         string `json:"plan"`
	SessionToken string `json:"sessionToken"`
}

// LoginGateway authenticates or registers an email with the web gateway
func (a *App) LoginGateway(email string) (*GatewayUser, error) {
	body, _ := json.Marshal(map[string]string{"email": email})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "POST", gatewayURL()+"/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := gatewayHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to reach gateway: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Success bool        `json:"success"`
		User    GatewayUser `json:"user"`
		Error   string      `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("invalid response from gateway")
	}
	if !result.Success {
		return nil, fmt.Errorf("login failed: %s", result.Error)
	}

	a.userTokens = result.User.Tokens
	return &result.User, nil
}

// GetGatewayProfile fetches the current profile by session token
func (a *App) GetGatewayProfile(token string) (*GatewayUser, error) {
	if token == "" {
		return nil, fmt.Errorf("token required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/api/auth/me?token=%s", gatewayURL(), token), nil)
	resp, err := gatewayHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("profile not found or expired")
	}

	var user GatewayUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, err
	}
	user.SessionToken = token
	a.userTokens = user.Tokens
	return &user, nil
}

// VisionStatus represents the combined status of vision eyes (local and cloud)
type VisionStatus struct {
	Online         bool   `json:"online"`
	HasCloudKey    bool   `json:"hasCloudKey"`
	HasGroqKey     bool   `json:"hasGroqKey"`
	HasGeminiKey   bool   `json:"hasGeminiKey"`
	GroqApiKey     string `json:"groqApiKey"`
	GeminiApiKey   string `json:"geminiApiKey"`
	VisionModel    string `json:"visionModel"`
	ActiveProvider string `json:"activeProvider"`
	HasLocalEyes   bool   `json:"hasLocalEyes"`
	LocalEyesName  string `json:"localEyesName"`
	LocalEyesTag   string `json:"localEyesTag"`
}

// GetVisionStatus returns whether local or cloud vision is ready to process images
func (a *App) GetVisionStatus() VisionStatus {
	st := VisionStatus{
		Online: a.isOnline,
	}
	if vd, ok := a.findVisionHelper(); ok {
		st.HasLocalEyes = true
		st.LocalEyesName = vd.Name
		st.LocalEyesTag = vd.OllamaTag
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", gatewayURL()+"/api/vision/config", nil)
	if err == nil {
		resp, rerr := gatewayHTTP.Do(req)
		if rerr == nil {
			defer resp.Body.Close()
			if resp.StatusCode == 200 {
				var cfg struct {
					HasGroqKey     bool   `json:"hasGroqKey"`
					HasGeminiKey   bool   `json:"hasGeminiKey"`
					GroqApiKey     string `json:"groqApiKey"`
					GeminiApiKey   string `json:"geminiApiKey"`
					VisionModel    string `json:"visionModel"`
					ActiveProvider string `json:"activeProvider"`
				}
				if json.NewDecoder(resp.Body).Decode(&cfg) == nil {
					st.HasGroqKey = cfg.HasGroqKey
					st.HasGeminiKey = cfg.HasGeminiKey
					st.GroqApiKey = cfg.GroqApiKey
					st.GeminiApiKey = cfg.GeminiApiKey
					st.HasCloudKey = cfg.HasGroqKey || cfg.HasGeminiKey
					st.VisionModel = cfg.VisionModel
					st.ActiveProvider = cfg.ActiveProvider
				}
			}
		}
	}
	return st
}

// SaveVisionConfig persists cloud vision API keys to the gateway
func (a *App) SaveVisionConfig(groqApiKey, geminiApiKey, visionModel string) error {
	body, _ := json.Marshal(map[string]string{
		"groqApiKey":   groqApiKey,
		"geminiApiKey": geminiApiKey,
		"visionModel":  visionModel,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", gatewayURL()+"/api/vision/config", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := gatewayHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("failed to save vision configuration (%d)", resp.StatusCode)
	}
	return nil
}
