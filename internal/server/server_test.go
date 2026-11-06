package server

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/1Solon/reel-to-tandoor/internal/config"
	"github.com/1Solon/reel-to-tandoor/internal/reel"
)

const reelURL = "https://www.instagram.com/reel/DfXD-34Bf40/"

type fakeMedia struct {
	downloadErr error
	image       *reel.Image
}

func (f *fakeMedia) Download(ctx context.Context, url, dir string) (reel.Reel, error) {
	if f.downloadErr != nil {
		return reel.Reel{}, f.downloadErr
	}
	return reel.Reel{URL: url, Caption: "Comment yes! #food", Uploader: "cameronjames.wellness",
		ThumbnailURL: "https://cdn.test/thumb.jpg", AudioPath: dir + "/a.m4a"}, nil
}

func (f *fakeMedia) DecodeAudio(ctx context.Context, path string) ([]float32, error) {
	return make([]float32, 16000), nil
}

func (f *fakeMedia) FetchThumbnail(ctx context.Context, url string) *reel.Image { return f.image }

type fakeTranscriber struct{}

func (fakeTranscriber) Transcribe(ctx context.Context, samples []float32) (string, error) {
	return "Soak two cups of red lentils overnight. Bake at 350 for 45 minutes.", nil
}

// fakeTandoor records requests and answers with canned responses keyed by "METHOD path".
type fakeTandoor struct {
	t         *testing.T
	mu        sync.Mutex
	responses map[string]func(w http.ResponseWriter)
	requests  map[string]*recordedRequest
}

type recordedRequest struct {
	header http.Header
	host   string
	body   []byte
	form   map[string]string
	files  map[string]string // field -> filename
}

func newFakeTandoor(t *testing.T) (*fakeTandoor, *httptest.Server) {
	f := &fakeTandoor{t: t, responses: map[string]func(http.ResponseWriter){}, requests: map[string]*recordedRequest{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeTandoor) on(route string, status int, body string) {
	f.responses[route] = func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}
}

func (f *fakeTandoor) serve(w http.ResponseWriter, r *http.Request) {
	route := r.Method + " " + r.URL.Path
	rec := &recordedRequest{header: r.Header.Clone(), host: r.Host, form: map[string]string{}, files: map[string]string{}}
	mediaType, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if strings.HasPrefix(mediaType, "multipart/") {
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			data, _ := io.ReadAll(part)
			if part.FileName() != "" {
				rec.files[part.FormName()] = part.FileName()
			} else {
				rec.form[part.FormName()] = string(data)
			}
		}
	} else {
		rec.body, _ = io.ReadAll(r.Body)
	}
	f.mu.Lock()
	f.requests[route] = rec
	respond := f.responses[route]
	f.mu.Unlock()
	if respond == nil {
		f.t.Errorf("unexpected request %s", route)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	respond(w)
}

func (f *fakeTandoor) request(route string) *recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[route]
}

func fixture(t *testing.T) string {
	data, err := os.ReadFile("../recipe/testdata/ai_import_lentil_bread.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func newServer(t *testing.T, tandoorURL string, media *fakeMedia) http.Handler {
	cfg := config.Config{
		TandoorURL:          tandoorURL,
		TandoorHostHeader:   "recipes.example.com",
		TandoorPublicURL:    "https://recipes.example.com",
		TandoorAIProviderID: 34,
		TandoorTimeout:      5e9,
		AllowTikTok:         true,
		Users:               map[string]config.User{"mum-key": {Name: "mum", TandoorToken: "tda_mum"}},
	}
	return New(cfg, media, fakeTranscriber{}).Handler()
}

func post(t *testing.T, h http.Handler, key, url string) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"url": url})
	req := httptest.NewRequest(http.MethodPost, "/save", strings.NewReader(string(body)))
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response isn't JSON: %q", rec.Body.String())
	}
	return rec.Code, out
}

func TestRejectsBadAPIKey(t *testing.T) {
	h := newServer(t, "http://unused", &fakeMedia{})
	for _, key := range []string{"", "wrong", "mum-ke"} {
		code, body := post(t, h, key, reelURL)
		if code != http.StatusUnauthorized || body["ok"] != false {
			t.Errorf("key %q: %d %v", key, code, body)
		}
	}
}

func TestRejectsNonInstagramLink(t *testing.T) {
	code, body := post(t, newServer(t, "http://unused", &fakeMedia{}), "mum-key", "https://example.com/video")
	if code != http.StatusBadRequest || body["message"] != "Only Instagram and TikTok links are supported." {
		t.Errorf("%d %v", code, body)
	}
}

func TestRejectsMalformedBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/save", strings.NewReader("not json"))
	req.Header.Set("X-API-Key", "mum-key")
	rec := httptest.NewRecorder()
	newServer(t, "http://unused", &fakeMedia{}).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestSavesRecipe(t *testing.T) {
	tandoor, srv := newFakeTandoor(t)
	tandoor.on("POST /api/ai-import/", 200, fixture(t))
	tandoor.on("POST /api/recipe/", 201, `{"id": 42, "name": "Flourless Lentil Bread"}`)
	tandoor.on("PUT /api/recipe/42/image/", 200, `{}`)
	h := newServer(t, srv.URL, &fakeMedia{image: &reel.Image{Data: []byte("\xff\xd8jpeg"), ContentType: "image/jpeg"}})

	code, body := post(t, h, "mum-key", reelURL+"?igsh=abc")

	want := map[string]any{
		"ok": true, "message": "Saved: Flourless Lentil Bread",
		"recipe_id": 42.0, "name": "Flourless Lentil Bread", "recipe_url": "https://recipes.example.com/recipe/42",
	}
	if code != http.StatusOK || len(body) != len(want) {
		t.Fatalf("%d %v", code, body)
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("%s = %v, want %v", k, body[k], v)
		}
	}

	ai := tandoor.request("POST /api/ai-import/")
	if ai.header.Get("Authorization") != "Bearer tda_mum" || ai.host != "recipes.example.com" {
		t.Errorf("auth/host = %q %q", ai.header.Get("Authorization"), ai.host)
	}
	if ai.form["ai_provider_id"] != "34" || ai.form["recipe_id"] != "" || ai.form["file"] != "" {
		t.Errorf("form = %v", ai.form)
	}
	if !strings.Contains(ai.form["text"], "Soak two cups of red lentils") || strings.Contains(ai.form["text"], "#food") {
		t.Errorf("text = %q", ai.form["text"])
	}

	var saved map[string]any
	json.Unmarshal(tandoor.request("POST /api/recipe/").body, &saved)
	if saved["source_url"] != reelURL || saved["name"] != "Flourless Lentil Bread" {
		t.Errorf("saved = %v", saved)
	}
	if got := tandoor.request("PUT /api/recipe/42/image/").files["image"]; got != "cover.jpg" {
		t.Errorf("image filename = %q", got)
	}
}

func TestEmptyAIRecipeIsReported(t *testing.T) {
	tandoor, srv := newFakeTandoor(t)
	tandoor.on("POST /api/ai-import/", 200, `{"recipe": {"name": "", "steps": [], "keywords": []}, "error": false, "msg": ""}`)

	code, body := post(t, newServer(t, srv.URL, &fakeMedia{}), "mum-key", reelURL)

	if code != http.StatusUnprocessableEntity || body["message"] != "No recipe found in this reel." {
		t.Errorf("%d %v", code, body)
	}
	if tandoor.request("POST /api/recipe/") != nil {
		t.Error("an empty recipe was saved")
	}
}

func TestTandoorFailuresAreReported(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{400, `{"error": true, "msg": "Error parsing AI results"}`, "Tandoor's AI couldn't read a recipe from this reel."},
		{401, `{"detail": "Invalid token."}`, "Tandoor rejected this user's token. Ask Saul to update it."},
		{429, `{"detail": "Request was throttled."}`, "Too many recipe imports this hour. Try again later."},
		{500, `oops`, "Tandoor couldn't run the AI import (HTTP 500)."},
	}
	for _, c := range cases {
		tandoor, srv := newFakeTandoor(t)
		tandoor.on("POST /api/ai-import/", c.status, c.body)
		code, body := post(t, newServer(t, srv.URL, &fakeMedia{}), "mum-key", reelURL)
		if code != http.StatusBadGateway || body["message"] != c.want {
			t.Errorf("HTTP %d: got %d %v", c.status, code, body)
		}
	}
}

func TestImageFailureStillSaves(t *testing.T) {
	tandoor, srv := newFakeTandoor(t)
	tandoor.on("POST /api/ai-import/", 200, fixture(t))
	tandoor.on("POST /api/recipe/", 201, `{"id": 7, "name": "Bread"}`)
	tandoor.on("PUT /api/recipe/7/image/", 400, `{"image": ["bad"]}`)
	h := newServer(t, srv.URL, &fakeMedia{image: &reel.Image{Data: []byte("x"), ContentType: "image/webp"}})
	if code, body := post(t, h, "mum-key", reelURL); code != http.StatusOK {
		t.Errorf("%d %v", code, body)
	}
	if got := tandoor.request("PUT /api/recipe/7/image/").files["image"]; got != "cover.webp" {
		t.Errorf("image filename = %q", got)
	}
}

func TestDownloadFailureIsReported(t *testing.T) {
	media := &fakeMedia{downloadErr: &reel.DownloadError{}}
	code, body := post(t, newServer(t, "http://unused", media), "mum-key", reelURL)
	if code != http.StatusBadGateway || body["ok"] != false {
		t.Errorf("%d %v", code, body)
	}
}

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	newServer(t, "http://unused", &fakeMedia{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
		t.Errorf("%d %q", rec.Code, rec.Body.String())
	}
}
