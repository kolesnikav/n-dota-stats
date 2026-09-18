package telegram

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Отправка картинки собирается вручную из multipart, и ошибиться тут легко:
// не то имя поля, не тот content-type, забытая клавиатура. Проверяем на
// поддельном сервере, что уходит именно то, что ждёт телеграм.
func TestSendPhotoRequest(t *testing.T) {
	var (
		gotPath   string
		gotFields = map[string]string{}
		gotPhoto  []byte
		gotName   string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			t.Errorf("тип содержимого: %v", err)
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			body, _ := io.ReadAll(part)
			if part.FormName() == "photo" {
				gotPhoto, gotName = body, part.FileName()
				continue
			}
			gotFields[part.FormName()] = string(body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":77}}`))
	}))
	defer srv.Close()

	b := New("TOKEN")
	b.api = srv.URL + "/bot%s/%s"
	kb := Keyboard{{{Text: "карта линии", Data: "k:1:lane"}}}
	id, err := b.SendPhoto(42, "подпись", "map.jpg", []byte("\xff\xd8\xff картинка"), kb)
	if err != nil {
		t.Fatalf("отправка: %v", err)
	}
	if id != 77 {
		t.Errorf("номер сообщения %d, ждали 77", id)
	}
	if !strings.HasSuffix(gotPath, "/sendPhoto") {
		t.Errorf("метод %q", gotPath)
	}
	if gotFields["chat_id"] != "42" {
		t.Errorf("chat_id %q", gotFields["chat_id"])
	}
	if gotFields["caption"] != "подпись" || gotFields["parse_mode"] != "HTML" {
		t.Errorf("подпись %q, разметка %q", gotFields["caption"], gotFields["parse_mode"])
	}
	if gotName != "map.jpg" || len(gotPhoto) == 0 {
		t.Errorf("файл %q, байт %d", gotName, len(gotPhoto))
	}
	var markup struct {
		Keyboard [][]Button `json:"inline_keyboard"`
	}
	if err := json.Unmarshal([]byte(gotFields["reply_markup"]), &markup); err != nil {
		t.Fatalf("клавиатура не разбирается: %v", err)
	}
	if len(markup.Keyboard) != 1 || markup.Keyboard[0][0].Data != "k:1:lane" {
		t.Errorf("клавиатура ушла как %q", gotFields["reply_markup"])
	}
}
