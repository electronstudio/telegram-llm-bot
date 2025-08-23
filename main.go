package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
	"gopkg.in/telebot.v3"
)

type Config struct {
	TelegramToken string `json:"telegram_token"`
	OpenAIAPIKey  string `json:"openai_api_key"`
	OpenAIAPIURL  string `json:"openai_api_url"`
	OpenAIModel   string `json:"openai_model"`
}

type Message struct {
	Username  string
	Text      string
	Timestamp time.Time
	IsBot     bool
}

type ConversationContext struct {
	Messages        []Message
	SystemMessage   string
	PendingMessages []Message
	LastMessageTime time.Time
	Timer           *time.Timer
	Mutex           sync.Mutex
}

type OpenAIRequest struct {
	Model    string          `json:"model"`
	Messages []OpenAIMessage `json:"messages"`
}

type OpenAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type OpenAIResponse struct {
	Choices []struct {
		Message OpenAIMessage `json:"message"`
	} `json:"choices"`
}

func loadConfig() (Config, error) {
	var config Config

	file, err := os.Open("config.json")
	if err != nil {
		return config, fmt.Errorf("failed to open config.json: %v", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	err = decoder.Decode(&config)
	if err != nil {
		return config, fmt.Errorf("failed to parse config.json: %v", err)
	}

	if config.TelegramToken == "" {
		return config, fmt.Errorf("telegram_token is required")
	}
	if config.OpenAIAPIKey == "" {
		return config, fmt.Errorf("openai_api_key is required")
	}
	if config.OpenAIAPIURL == "" {
		return config, fmt.Errorf("openai_api_url is required")
	}
	if config.OpenAIModel == "" {
		return config, fmt.Errorf("openai_model is required")
	}

	return config, nil
}

func callOpenAI(config Config, messages []OpenAIMessage) (string, error) {
	client := resty.New()

	request := OpenAIRequest{
		Model:    config.OpenAIModel,
		Messages: messages,
	}

	var response OpenAIResponse

	resp, err := client.R().
		SetHeader("Authorization", "Bearer "+config.OpenAIAPIKey).
		SetHeader("Content-Type", "application/json").
		SetBody(request).
		SetResult(&response).
		Post(config.OpenAIAPIURL)

	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %v", err)
	}

	if resp.StatusCode() != 200 {
		return "", fmt.Errorf("API returned status %d: %s", resp.StatusCode(), resp.String())
	}

	if len(response.Choices) == 0 {
		return "", fmt.Errorf("no choices in API response")
	}

	return response.Choices[0].Message.Content, nil
}

func formatMessagesForContext(context *ConversationContext) []OpenAIMessage {
	var openAIMessages []OpenAIMessage

	openAIMessages = append(openAIMessages, OpenAIMessage{
		Role:    "system",
		Content: context.SystemMessage,
	})

	for _, msg := range context.Messages {
		if msg.IsBot {
			openAIMessages = append(openAIMessages, OpenAIMessage{
				Role:    "assistant",
				Content: msg.Text,
			})
		} else {
			openAIMessages = append(openAIMessages, OpenAIMessage{
				Role:    "user",
				Content: fmt.Sprintf("%s: %s", msg.Username, msg.Text),
			})
		}
	}

	for _, msg := range context.PendingMessages {
		openAIMessages = append(openAIMessages, OpenAIMessage{
			Role:    "user",
			Content: fmt.Sprintf("%s: %s", msg.Username, msg.Text),
		})
	}

	return openAIMessages
}

func trimContext(context *ConversationContext, maxChars int) {
	for {
		totalChars := 0

		for _, msg := range context.Messages {
			if msg.IsBot {
				totalChars += len(msg.Text)
			} else {
				totalChars += len(fmt.Sprintf("%s: %s", msg.Username, msg.Text))
			}
		}

		if totalChars <= maxChars || len(context.Messages) == 0 {
			break
		}

		context.Messages = context.Messages[1:]
	}
}

func addToContext(context *ConversationContext, username string, text string, isBot bool) {
	message := Message{
		Username:  username,
		Text:      text,
		Timestamp: time.Now(),
		IsBot:     isBot,
	}

	context.Messages = append(context.Messages, message)
	trimContext(context, 8000)
}

func handleIncomingMessage(bot *telebot.Bot, context *ConversationContext, config Config, m *telebot.Message) {
	if m.Text == "" || strings.TrimSpace(m.Text) == "" {
		return
	}

	if m.Sender.ID == bot.Me.ID {
		return
	}

	context.Mutex.Lock()
	defer context.Mutex.Unlock()

	username := m.Sender.Username
	if username == "" {
		username = m.Sender.FirstName
		if m.Sender.LastName != "" {
			username += " " + m.Sender.LastName
		}
	}

	message := Message{
		Username:  username,
		Text:      m.Text,
		Timestamp: time.Now(),
		IsBot:     false,
	}

	context.PendingMessages = append(context.PendingMessages, message)

	if context.Timer != nil {
		context.Timer.Stop()
	}

	context.Timer = time.AfterFunc(10*time.Second, func() {
		processBatch(bot, m.Chat, context, config)
	})
}

func processBatch(bot *telebot.Bot, chat *telebot.Chat, context *ConversationContext, config Config) {
	context.Mutex.Lock()

	if len(context.PendingMessages) == 0 {
		context.Mutex.Unlock()
		return
	}

	for _, msg := range context.PendingMessages {
		context.Messages = append(context.Messages, msg)
	}

	openAIMessages := formatMessagesForContext(context)
	context.PendingMessages = []Message{}
	context.Timer = nil

	context.Mutex.Unlock()

	bot.Notify(chat, telebot.Typing)

	response, err := callOpenAI(config, openAIMessages)
	if err != nil {
		log.Printf("OpenAI API error: %v", err)
		return
	}

	if len(response) > 4096 {
		response = response[:4096]
	}

	_, err = bot.Send(chat, response)
	if err != nil {
		log.Printf("Telegram send error: %v", err)
		return
	}

	context.Mutex.Lock()
	addToContext(context, "bot", response, true)
	context.Mutex.Unlock()
}

func main() {
	config, err := loadConfig()
	if err != nil {
		log.Fatal("Configuration error:", err)
	}

	context := ConversationContext{
		Messages: []Message{},
		SystemMessage: `You are a human man named Frank.  Your interests are WWE wrestling, 
		guitars, Nintendo, the band Bloc Party.  You do not like transexuals.  You are participating in a group chat. 
	Following this are the most recent messages in the group chat.  The format of each message is:
	[username]: [message text]
	`,
		PendingMessages: []Message{},
		Timer:           nil,
	}

	pref := telebot.Settings{
		Token:  config.TelegramToken,
		Poller: &telebot.LongPoller{Timeout: 10 * time.Second},
	}

	bot, err := telebot.NewBot(pref)
	if err != nil {
		log.Fatal("Bot creation error:", err)
	}

	bot.Handle(telebot.OnText, func(c telebot.Context) error {
		message := c.Message()

		if message.Sender.ID == bot.Me.ID {
			return nil
		}

		go handleIncomingMessage(bot, &context, config, message)
		return nil
	})

	log.Println("Bot starting...")
	bot.Start()
}
