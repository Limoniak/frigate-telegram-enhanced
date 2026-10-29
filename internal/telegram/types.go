package telegram

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
	MessageID int         `json:"message_id"`
	From      *User       `json:"from"`
	Chat      Chat        `json:"chat"`
	Text      string      `json:"text"`
	Photo     []PhotoSize `json:"photo"`
	Video     *Video      `json:"video"`
	Animation *Animation  `json:"animation"`
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
	return ""
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
	// Width, Height and Duration (seconds) of a video, sent when not 0: without them
	// Telegram guesses them from the upload.
	Width, Height, Duration int
}

// videoFields returns the size and duration of a video that are known.
func (f InputFile) videoFields() map[string]int {
	out := map[string]int{}
	for k, v := range map[string]int{"width": f.Width, "height": f.Height, "duration": f.Duration} {
		if v > 0 {
			out[k] = v
		}
	}
	return out
}

// SendOptions gathers the common send options. Caption is ignored by SendMessage.
type SendOptions struct {
	Caption string
	Silent  bool
	ReplyTo int
	Markup  *InlineKeyboardMarkup
}
