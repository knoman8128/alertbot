package telegrambot

import (
	"log"
	"strconv"
	"time"

	tele "gopkg.in/telebot.v3"
)

// TelegramBot for slack based control
type TelegramBot struct {
	user        *tele.User
	bot         *tele.Bot
	sendOptions *tele.SendOptions
	handlers    map[string]func(string)
}

// New create TelegramBot
func New(userID string, token string) *TelegramBot {
	_userID, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		log.Fatal(err)
		return nil
	}

	pref := tele.Settings{
		Token:  token,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	}

	b, err := tele.NewBot(pref)
	if err != nil {
		log.Fatal(err)
		return nil
	}

	return &TelegramBot{
		user:        &tele.User{ID: _userID},
		bot:         b,
		sendOptions: &tele.SendOptions{ParseMode: tele.ModeHTML, DisableWebPagePreview: true},
		handlers:    make(map[string]func(string)),
	}
}

// Start listen events
func (tb *TelegramBot) Start() {
	tb.RegisterCommand("/working", func(s string) { tb.PostMessage("Yes!") })

	tb.bot.Start()
}

// RegisterCommand for a slash command
func (tb *TelegramBot) RegisterCommand(command string, handler func(string)) {
	if _, found := tb.handlers[command]; found {
		log.Printf("%s command already registered\n", command)
		return
	}

	tb.bot.Handle(command, func(c tele.Context) error {
		handler(c.Message().Payload)
		return nil
	})

	tb.handlers[command] = handler
}

// RegisterCommands for slash commands
func (tb *TelegramBot) RegisterCommands(commands []string, handler func(string)) {
	for _, command := range commands {
		tb.RegisterCommand(command, handler)
	}
}

// PostMessage for message sending
func (tb *TelegramBot) PostMessage(message string) {
	tb.bot.Send(tb.user, message, tb.sendOptions)
}
