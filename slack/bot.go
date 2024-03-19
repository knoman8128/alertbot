package slackbot

import (
	"context"
	"log"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

// SlackBot for slack based control
type SlackBot struct {
	client       *slack.Client
	socketClient *socketmode.Client
	context      context.Context
	handlers     map[string]func(string)
}

// New create SlackBot
func New(token string, appToken string) *SlackBot {
	_client := slack.New(token, slack.OptionDebug(false), slack.OptionAppLevelToken(appToken))
	return &SlackBot{
		client:       _client,
		socketClient: socketmode.New(_client, socketmode.OptionDebug(false)),
		context:      context.Background(),
		handlers:     make(map[string]func(string)),
	}
}

// Start listen events
func (sb *SlackBot) Start() {
	go func(ctx context.Context, client *slack.Client, socketClient *socketmode.Client) {
		for {
			select {
			case <-ctx.Done():
				log.Println("Shutting down socketmode listener")
				return
			case event := <-socketClient.Events:
				switch event.Type {
				case socketmode.EventTypeSlashCommand:
					command, ok := event.Data.(slack.SlashCommand)
					if !ok {
						log.Printf("Could not type cast the message to a SlashCommand: %v\n", command)
						continue
					}
					socketClient.Ack(*event.Request)
					sb.handleSlashCommand(command.Command, command.Text)
				}
			}

		}
	}(sb.context, sb.client, sb.socketClient)

	sb.socketClient.Run()
}

// RegisterHandler for a slash command
func (sb *SlackBot) RegisterHandler(command string, handler func(string)) {
	if _, found := sb.handlers[command]; found {
		log.Printf("%s command already registered\n", command)
		return
	}

	sb.handlers[command] = handler
}

// PostMessage for message sending
func (sb *SlackBot) PostMessage(channelID string, message string) {
	if _, _, err := sb.client.PostMessage(channelID, slack.MsgOptionText(message, false)); err != nil {
		log.Printf("Failed to send message on channel %s\n", channelID)
	}
}

func (sb *SlackBot) handleSlashCommand(command string, content string) {
	if _, found := sb.handlers[command]; !found {
		return
	}

	sb.handlers[command](content)
}
