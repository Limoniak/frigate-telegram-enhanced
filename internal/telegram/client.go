package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.telegram.org"

// APIError est une réponse d'erreur de l'API Bot.
type APIError struct {
	Method      string
	Code        int
	Description string
	RetryAfter  time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
}

type Client struct {
	token   string
	baseURL string
	http    *http.Client
	limiter *Limiter
	backoff time.Duration
	retries int
	onError func(method string, code int)
}

type Option func(*Client)

func WithBaseURL(u string) Option                          { return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") } }
func WithBackoff(d time.Duration) Option                   { return func(c *Client) { c.backoff = d } }
func WithLimiter(l *Limiter) Option                        { return func(c *Client) { c.limiter = l } }
func WithErrorHook(f func(method string, code int)) Option { return func(c *Client) { c.onError = f } }

func New(token string, opts ...Option) *Client {
	c := &Client{
		token:   token,
		baseURL: defaultBaseURL,
		http:    &http.Client{},
		limiter: NewLimiter(),
		backoff: time.Second,
		retries: 3,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

type request struct {
	method  string
	chatID  int64 // 0 : pas de limitation de débit
	params  map[string]string
	file    *fileParam
	timeout time.Duration
	noRetry bool
}

type fileParam struct {
	field string
	file  InputFile
}

func (c *Client) call(ctx context.Context, r request, out any) error {
	if r.timeout == 0 {
		r.timeout = 30 * time.Second
	}
	var err error
	for attempt := 0; ; attempt++ {
		if r.chatID != 0 {
			if err = c.limiter.Wait(ctx, r.chatID); err != nil {
				return err
			}
		}
		if err = c.once(ctx, r, out); err == nil {
			return nil
		}
		delay, retry := c.retryDelay(ctx, err, attempt)
		if !retry || r.noRetry || attempt >= c.retries {
			break
		}
		if serr := sleep(ctx, delay); serr != nil {
			return serr
		}
	}
	if c.onError != nil && ctx.Err() == nil {
		code := 0
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			code = apiErr.Code
		}
		c.onError(r.method, code)
	}
	return err
}

func (c *Client) retryDelay(ctx context.Context, err error, attempt int) (time.Duration, bool) {
	if ctx.Err() != nil {
		return 0, false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Code == http.StatusTooManyRequests:
			d := apiErr.RetryAfter
			if d <= 0 {
				d = c.backoff
			}
			return min(d, time.Minute), true
		case apiErr.Code >= 500:
			return c.backoff << attempt, true
		default:
			return 0, false
		}
	}
	return c.backoff << attempt, true // erreur réseau
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func (c *Client) once(ctx context.Context, r request, out any) error {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	body, size, contentType, err := r.body()
	if err != nil {
		return err
	}
	defer body.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bot"+c.token+"/"+r.method, body)
	if err != nil {
		return errors.New(c.redact(err.Error()))
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", contentType)
	resp, err := c.http.Do(req)
	if err != nil {
		// L'erreur contient l'URL, donc le token : on le masque.
		return fmt.Errorf("telegram %s: %s", r.method, c.redact(err.Error()))
	}
	defer resp.Body.Close()
	var ar apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return &APIError{Method: r.method, Code: resp.StatusCode, Description: "réponse illisible"}
	}
	if !ar.OK {
		e := &APIError{Method: r.method, Code: ar.ErrorCode, Description: ar.Description}
		if e.Code == 0 {
			e.Code = resp.StatusCode
		}
		if ar.Parameters != nil {
			e.RetryAfter = time.Duration(ar.Parameters.RetryAfter) * time.Second
		}
		return e
	}
	if out != nil && len(ar.Result) > 0 {
		if err := json.Unmarshal(ar.Result, out); err != nil {
			return fmt.Errorf("telegram %s: %w", r.method, err)
		}
	}
	return nil
}

func (c *Client) redact(s string) string { return strings.ReplaceAll(s, c.token, "<token>") }

// body construit le corps : formulaire simple, ou multipart de taille connue (pas de chunked).
func (r request) body() (io.ReadCloser, int64, string, error) {
	if r.file == nil || r.file.file.FileID != "" {
		v := url.Values{}
		for k, val := range r.params {
			v.Set(k, val)
		}
		if r.file != nil {
			v.Set(r.file.field, r.file.file.FileID)
		}
		s := v.Encode()
		return io.NopCloser(strings.NewReader(s)), int64(len(s)), "application/x-www-form-urlencoded", nil
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, k := range slices.Sorted(maps.Keys(r.params)) {
		if err := mw.WriteField(k, r.params[k]); err != nil {
			return nil, 0, "", err
		}
	}
	if _, err := mw.CreateFormFile(r.file.field, r.file.file.Name); err != nil {
		return nil, 0, "", err
	}
	head := bytes.Clone(buf.Bytes())
	buf.Reset()
	if err := mw.Close(); err != nil {
		return nil, 0, "", err
	}
	tail := bytes.Clone(buf.Bytes())

	content, size, closer, err := r.file.file.open()
	if err != nil {
		return nil, 0, "", err
	}
	rc := struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(head), content, bytes.NewReader(tail)), closer}
	return rc, int64(len(head)) + size + int64(len(tail)), mw.FormDataContentType(), nil
}

func (f InputFile) open() (io.Reader, int64, io.Closer, error) {
	if f.Path == "" {
		return bytes.NewReader(f.Data), int64(len(f.Data)), io.NopCloser(nil), nil
	}
	file, err := os.Open(f.Path)
	if err != nil {
		return nil, 0, nil, err
	}
	st, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, nil, err
	}
	return file, st.Size(), file, nil
}

func chatParam(id int64) string { return strconv.FormatInt(id, 10) }

func (o SendOptions) apply(p map[string]string) {
	p["parse_mode"] = "HTML"
	if o.Caption != "" {
		p["caption"] = o.Caption
	}
	if o.Silent {
		p["disable_notification"] = "true"
	}
	if o.ReplyTo != 0 {
		p["reply_parameters"] = fmt.Sprintf(`{"message_id":%d,"allow_sending_without_reply":true}`, o.ReplyTo)
	}
	if o.Markup != nil {
		b, _ := json.Marshal(o.Markup)
		p["reply_markup"] = string(b)
	}
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, o SendOptions) (Message, error) {
	p := map[string]string{"chat_id": chatParam(chatID), "text": text, "link_preview_options": `{"is_disabled":true}`}
	o.Caption = ""
	o.apply(p)
	var m Message
	err := c.call(ctx, request{method: "sendMessage", chatID: chatID, params: p}, &m)
	return m, err
}

func (c *Client) SendPhoto(ctx context.Context, chatID int64, f InputFile, o SendOptions) (Message, error) {
	return c.sendMedia(ctx, "sendPhoto", "photo", chatID, f, o, nil)
}

func (c *Client) SendVideo(ctx context.Context, chatID int64, f InputFile, o SendOptions) (Message, error) {
	return c.sendMedia(ctx, "sendVideo", "video", chatID, f, o, map[string]string{"supports_streaming": "true"})
}

func (c *Client) SendAnimation(ctx context.Context, chatID int64, f InputFile, o SendOptions) (Message, error) {
	return c.sendMedia(ctx, "sendAnimation", "animation", chatID, f, o, nil)
}

func (c *Client) sendMedia(ctx context.Context, method, field string, chatID int64, f InputFile, o SendOptions, extra map[string]string) (Message, error) {
	p := map[string]string{"chat_id": chatParam(chatID)}
	maps.Copy(p, extra)
	o.apply(p)
	timeout := 30 * time.Second
	if f.FileID == "" {
		timeout = 5 * time.Minute // upload jusqu'à 50 Mo
	}
	var m Message
	err := c.call(ctx, request{method: method, chatID: chatID, params: p, file: &fileParam{field: field, file: f}, timeout: timeout}, &m)
	return m, err
}

func (c *Client) EditMessageCaption(ctx context.Context, chatID int64, messageID int, caption string, markup *InlineKeyboardMarkup) error {
	p := map[string]string{"chat_id": chatParam(chatID), "message_id": strconv.Itoa(messageID)}
	SendOptions{Caption: caption, Markup: markup}.apply(p)
	return c.call(ctx, request{method: "editMessageCaption", chatID: chatID, params: p}, nil)
}

func (c *Client) EditMessageText(ctx context.Context, chatID int64, messageID int, text string, markup *InlineKeyboardMarkup) error {
	p := map[string]string{"chat_id": chatParam(chatID), "message_id": strconv.Itoa(messageID), "text": text, "link_preview_options": `{"is_disabled":true}`}
	SendOptions{Markup: markup}.apply(p)
	return c.call(ctx, request{method: "editMessageText", chatID: chatID, params: p}, nil)
}

func (c *Client) AnswerCallbackQuery(ctx context.Context, id, text string) error {
	p := map[string]string{"callback_query_id": id}
	if text != "" {
		p["text"] = text
	}
	return c.call(ctx, request{method: "answerCallbackQuery", params: p}, nil)
}

// GetUpdates fait un long polling ; pas de retry interne (la boucle appelante s'en charge).
func (c *Client) GetUpdates(ctx context.Context, offset int, timeout time.Duration) ([]Update, error) {
	p := map[string]string{
		"offset":          strconv.Itoa(offset),
		"timeout":         strconv.Itoa(int(timeout.Seconds())),
		"allowed_updates": `["message","callback_query"]`,
	}
	var ups []Update
	err := c.call(ctx, request{method: "getUpdates", params: p, timeout: timeout + 15*time.Second, noRetry: true}, &ups)
	return ups, err
}

func (c *Client) SetMyCommands(ctx context.Context, cmds []BotCommand) error {
	b, err := json.Marshal(cmds)
	if err != nil {
		return err
	}
	return c.call(ctx, request{method: "setMyCommands", params: map[string]string{"commands": string(b)}}, nil)
}

// GetMe renvoie le compte du bot ; sert à vérifier le token.
func (c *Client) GetMe(ctx context.Context) (User, error) {
	var u User
	err := c.call(ctx, request{method: "getMe", timeout: 10 * time.Second, noRetry: true}, &u)
	return u, err
}
