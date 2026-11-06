// Package tandoor makes the few Tandoor API calls the service needs.
package tandoor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/1Solon/reel-to-tandoor/internal/recipe"
)

// Error is a failed Tandoor call. Its message is safe to show to the user.
type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

func userError(format string, args ...any) error { return &Error{fmt.Sprintf(format, args...)} }

// Client talks to one Tandoor instance as one user.
type Client struct {
	baseURL    string
	token      string
	hostHeader string
	http       *http.Client
}

// New returns a client. hostHeader, when set, is sent as Host: Tandoor's ALLOWED_HOSTS rejects
// requests that don't name its public host.
func New(baseURL, token, hostHeader string, timeout time.Duration) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		hostHeader: hostHeader,
		http:       &http.Client{Timeout: timeout},
	}
}

// Recipe is the part of a saved recipe the service uses.
type Recipe struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func (c *Client) do(ctx context.Context, method, path, contentType string, body []byte) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", contentType)
	if c.hostHeader != "" {
		req.Host = c.hostHeader
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	return resp, data, err
}

func check(resp *http.Response, data []byte, action string) error {
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return userError("Tandoor rejected this user's token. Ask Saul to update it.")
	case resp.StatusCode == http.StatusTooManyRequests:
		return userError("Too many recipe imports this hour. Try again later.")
	case resp.StatusCode >= 400:
		slog.Warn("Tandoor call failed", "action", action, "status", resp.StatusCode, "body", truncate(data, 1000))
		return userError("Tandoor couldn't %s (HTTP %d).", action, resp.StatusCode)
	}
	return nil
}

func truncate(data []byte, n int) string {
	if len(data) > n {
		data = data[:n]
	}
	return string(data)
}

func isTimeout(err error) bool {
	var netErr interface{ Timeout() bool }
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout())
}

// AIImport sends text to /api/ai-import/ and returns the unsaved recipe.
func (c *Client) AIImport(ctx context.Context, text string, aiProviderID int) (recipe.AIRecipe, error) {
	// The endpoint only takes multipart; the empty recipe_id and file fields match what the web UI sends.
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, field := range [][2]string{{"recipe_id", ""}, {"file", ""}, {"text", text}, {"ai_provider_id", strconv.Itoa(aiProviderID)}} {
		if err := w.WriteField(field[0], field[1]); err != nil {
			return recipe.AIRecipe{}, err
		}
	}
	w.Close()

	resp, data, err := c.do(ctx, http.MethodPost, "/api/ai-import/", w.FormDataContentType(), buf.Bytes())
	if err != nil {
		if isTimeout(err) {
			return recipe.AIRecipe{}, userError("Tandoor's AI took too long to answer. Try again in a minute.")
		}
		slog.Warn("Tandoor AI import request failed", "err", err)
		return recipe.AIRecipe{}, userError("Couldn't reach Tandoor.")
	}

	var body struct {
		Error  bool             `json:"error"`
		Msg    string           `json:"msg"`
		Recipe *recipe.AIRecipe `json:"recipe"`
	}
	decodeErr := json.Unmarshal(data, &body)
	if body.Error {
		slog.Warn("Tandoor AI import error", "msg", truncate([]byte(body.Msg), 1000))
		return recipe.AIRecipe{}, userError("Tandoor's AI couldn't read a recipe from this reel.")
	}
	if err := check(resp, data, "run the AI import"); err != nil {
		return recipe.AIRecipe{}, err
	}
	if decodeErr != nil || body.Recipe == nil {
		slog.Warn("Tandoor AI import returned no recipe", "err", decodeErr, "body", truncate(data, 1000))
		return recipe.AIRecipe{}, userError("Tandoor's AI couldn't read a recipe from this reel.")
	}
	return *body.Recipe, nil
}

// CreateRecipe saves a recipe with POST /api/recipe/.
func (c *Client) CreateRecipe(ctx context.Context, payload recipe.Payload) (Recipe, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return Recipe{}, err
	}
	resp, data, err := c.do(ctx, http.MethodPost, "/api/recipe/", "application/json", body)
	if err != nil {
		slog.Warn("Tandoor create recipe request failed", "err", err)
		return Recipe{}, userError("Couldn't reach Tandoor.")
	}
	if err := check(resp, data, "save the recipe"); err != nil {
		return Recipe{}, err
	}
	var saved Recipe
	if err := json.Unmarshal(data, &saved); err != nil || saved.ID == 0 {
		slog.Warn("Tandoor create recipe returned no id", "err", err, "body", truncate(data, 1000))
		return Recipe{}, userError("Tandoor couldn't save the recipe.")
	}
	return saved, nil
}

// UploadImage attaches an image with PUT /api/recipe/{id}/image/. Tandoor checks the file
// name's extension, so it has to match the content type.
func (c *Client) UploadImage(ctx context.Context, recipeID int, image []byte, contentType string) error {
	ext := map[string]string{"image/png": "png", "image/webp": "webp"}[contentType]
	if ext == "" {
		ext = "jpg"
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image"; filename="cover.%s"`, ext))
	header.Set("Content-Type", contentType)
	part, err := w.CreatePart(header)
	if err != nil {
		return err
	}
	if _, err := part.Write(image); err != nil {
		return err
	}
	w.Close()

	resp, data, err := c.do(ctx, http.MethodPut, fmt.Sprintf("/api/recipe/%d/image/", recipeID), w.FormDataContentType(), buf.Bytes())
	if err != nil {
		slog.Warn("Tandoor image upload request failed", "err", err)
		return userError("Couldn't upload the recipe image.")
	}
	return check(resp, data, "save the recipe image")
}
