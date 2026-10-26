package telegram

import "encoding/json"

type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

type Chat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"` // groups and channels
}

type PhotoSize struct {
	FileID string `json:"file_id"`
}

type Video struct {
	FileID string `json:"file_id"`
}

type Animation struct {
	FileID string `json:"file_id"`
}

type Message struct {
	MessageID   int          `json:"message_id"`
	From        *User        `json:"from"`
	Chat        Chat         `json:"chat"`
	Text        string       `json:"text"`
	Photo       []PhotoSize  `json:"photo"`
	Video       *Video       `json:"video"`
	Animation   *Animation   `json:"animation"`
	RichMessage *RichMessage `json:"rich_message"`
}

// RichMessage is rich formatted content returned by Telegram.
type RichMessage struct {
	Blocks []json.RawMessage `json:"blocks"`
	IsRTL  bool              `json:"is_rtl"`
}

// InputRichMessage is the content accepted by sendRichMessage. Exactly one of
// HTML, Markdown, or Blocks must be set; Telegram validates the block shapes.
type InputRichMessage struct {
	Blocks              []json.RawMessage       `json:"blocks,omitempty"`
	HTML                string                  `json:"html,omitempty"`
	Markdown            string                  `json:"markdown,omitempty"`
	Media               []InputRichMessageMedia `json:"media,omitempty"`
	IsRTL               bool                    `json:"is_rtl,omitempty"`
	SkipEntityDetection bool                    `json:"skip_entity_detection,omitempty"`
}

// InputRichMessageMedia binds an uploaded or previously stored media object
// to a tg:// media link used in HTML or Markdown rich content.
type InputRichMessageMedia struct {
	ID    string          `json:"id"`
	Media json.RawMessage `json:"media"`
}

// FileID returns the ID of the media sent, reusable for another chat without a new upload.
func (m Message) FileID() string {
	switch {
	case m.Video != nil:
		return m.Video.FileID
	case m.Animation != nil:
		return m.Animation.FileID
	case len(m.Photo) > 0:
		return m.Photo[len(m.Photo)-1].FileID
	}
	for _, raw := range m.RichMessageBlocks() {
		var block struct {
			Photo     []PhotoSize `json:"photo"`
			Video     *Video      `json:"video"`
			Animation *Animation  `json:"animation"`
		}
		if json.Unmarshal(raw, &block) != nil {
			continue
		}
		if block.Video != nil {
			return block.Video.FileID
		}
		if block.Animation != nil {
			return block.Animation.FileID
		}
		if len(block.Photo) > 0 {
			return block.Photo[len(block.Photo)-1].FileID
		}
	}
	return ""
}

func (m Message) RichMessageBlocks() []json.RawMessage {
	if m.RichMessage == nil {
		return nil
	}
	return m.RichMessage.Blocks
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type Update struct {
	UpdateID      int            `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// InputFile designates a media: FileID (already on Telegram), Data (in memory) or Path (on disk).
type InputFile struct {
	FileID string
	Name   string
	Data   []byte
	Path   string
}

// SendOptions gathers the common send options. Caption is ignored by SendMessage.
type SendOptions struct {
	Caption string
	Silent  bool
	ReplyTo int
	Markup  *InlineKeyboardMarkup
}
