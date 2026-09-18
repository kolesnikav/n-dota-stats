// Package telegram — минимальный клиент Bot API на стандартной библиотеке.
package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"
)

// Bot — клиент Telegram Bot API.
type Bot struct {
	Token string
	HTTP  *http.Client
}

func New(token string) *Bot {
	return &Bot{Token: token, HTTP: &http.Client{Timeout: 70 * time.Second}}
}

// Button — кнопка inline-клавиатуры.
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}

// Keyboard — ряды кнопок.
type Keyboard [][]Button

// Update — входящее обновление.
type Update struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		MessageID int64  `json:"message_id"`
		Text      string `json:"text"`
		Chat      struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		From struct {
			ID        int64  `json:"id"`
			Username  string `json:"username"`
			FirstName string `json:"first_name"`
		} `json:"from"`
	} `json:"message"`
	CallbackQuery *struct {
		ID   string `json:"id"`
		Data string `json:"data"`
		From struct {
			ID int64 `json:"id"`
		} `json:"from"`
		Message *struct {
			MessageID int64 `json:"message_id"`
			Chat      struct {
				ID int64 `json:"id"`
			} `json:"chat"`
		} `json:"message"`
	} `json:"callback_query"`
}

func (b *Bot) call(method string, params map[string]any, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/%s", b.Token, method)
	resp, err := b.HTTP.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var envelope struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("%s: %s", method, truncate(string(raw), 200))
	}
	if !envelope.OK {
		return fmt.Errorf("%s: %s", method, envelope.Description)
	}
	if out != nil && len(envelope.Result) > 0 {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// GetUpdates забирает обновления длинным опросом.
func (b *Bot) GetUpdates(offset int64, timeout int) ([]Update, error) {
	var out []Update
	err := b.call("getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         timeout,
		"allowed_updates": []string{"message", "callback_query"},
	}, &out)
	return out, err
}

// Send отправляет сообщение и возвращает его id.
func (b *Bot) Send(chatID int64, text string, kb Keyboard) (int64, error) {
	params := map[string]any{
		"chat_id":              chatID,
		"text":                 text,
		"parse_mode":           "HTML",
		"link_preview_options": map[string]any{"is_disabled": true},
	}
	if kb != nil {
		params["reply_markup"] = map[string]any{"inline_keyboard": kb}
	}
	var out struct {
		MessageID int64 `json:"message_id"`
	}
	err := b.call("sendMessage", params, &out)
	return out.MessageID, err
}

// Edit заменяет текст и клавиатуру сообщения.
func (b *Bot) Edit(chatID, messageID int64, text string, kb Keyboard) error {
	params := map[string]any{
		"chat_id":              chatID,
		"message_id":           messageID,
		"text":                 text,
		"parse_mode":           "HTML",
		"link_preview_options": map[string]any{"is_disabled": true},
	}
	if kb != nil {
		params["reply_markup"] = map[string]any{"inline_keyboard": kb}
	} else {
		params["reply_markup"] = map[string]any{"inline_keyboard": Keyboard{}}
	}
	return b.call("editMessageText", params, nil)
}

// Answer закрывает «часики» на нажатой кнопке.
func (b *Bot) Answer(callbackID, text string) error {
	params := map[string]any{"callback_query_id": callbackID}
	if text != "" {
		params["text"] = text
	}
	return b.call("answerCallbackQuery", params, nil)
}

// SendPhoto отправляет картинку. Подпись идёт отдельным полем и ограничена
// телеграмом тысячей символов, поэтому длинный текст в неё не кладём.
func (b *Bot) SendPhoto(chatID int64, caption, filename string, data []byte, kb Keyboard) (int64, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("chat_id", strconv.FormatInt(chatID, 10))
	if caption != "" {
		_ = w.WriteField("caption", caption)
		_ = w.WriteField("parse_mode", "HTML")
	}
	if len(kb) > 0 {
		markup, err := json.Marshal(map[string]any{"inline_keyboard": kb})
		if err != nil {
			return 0, err
		}
		_ = w.WriteField("reply_markup", string(markup))
	}
	part, err := w.CreateFormFile("photo", filename)
	if err != nil {
		return 0, err
	}
	if _, err := part.Write(data); err != nil {
		return 0, err
	}
	if err := w.Close(); err != nil {
		return 0, err
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendPhoto", b.Token)
	resp, err := b.HTTP.Post(url, w.FormDataContentType(), &body)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	var envelope struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return 0, fmt.Errorf("sendPhoto: %s", truncate(string(raw), 200))
	}
	if !envelope.OK {
		return 0, fmt.Errorf("sendPhoto: %s", envelope.Description)
	}
	return envelope.Result.MessageID, nil
}
