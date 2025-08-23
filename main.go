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
	TelegramToken  string `json:"telegram_token"`
	OpenAIAPIKey   string `json:"openai_api_key"`
	OpenAIAPIURL   string `json:"openai_api_url"`
	OpenAIModel    string `json:"openai_model"`
	StartupMessage string `json:"startup_message"`
}

type BotStatus struct {
	ChatIDs []int64 `json:"chat_ids"`
	mutex   sync.Mutex
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

func loadBotStatus() (*BotStatus, error) {
	status := &BotStatus{
		ChatIDs: []int64{},
	}

	file, err := os.Open("status.json")
	if err != nil {
		if os.IsNotExist(err) {
			log.Println("status.json does not exist, will create on first chat interaction")
			return status, nil
		}
		return status, fmt.Errorf("failed to open status.json: %v", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	err = decoder.Decode(status)
	if err != nil {
		return status, fmt.Errorf("failed to parse status.json: %v", err)
	}

	log.Printf("Loaded status.json with %d chat IDs", len(status.ChatIDs))
	return status, nil
}

func (s *BotStatus) addChatID(chatID int64) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	for _, id := range s.ChatIDs {
		if id == chatID {
			return nil
		}
	}

	s.ChatIDs = append(s.ChatIDs, chatID)
	log.Printf("New chat added: %d (total: %d chats)", chatID, len(s.ChatIDs))
	return s.save()
}

func (s *BotStatus) removeChatID(chatID int64) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	for i, id := range s.ChatIDs {
		if id == chatID {
			s.ChatIDs = append(s.ChatIDs[:i], s.ChatIDs[i+1:]...)
			return s.save()
		}
	}

	return nil
}

func (s *BotStatus) save() error {
	file, err := os.Create("status.json")
	if err != nil {
		return fmt.Errorf("failed to create status.json: %v", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	err = encoder.Encode(s)
	if err != nil {
		return fmt.Errorf("failed to write status.json: %v", err)
	}

	log.Printf("Saved status.json with %d chat IDs", len(s.ChatIDs))
	return nil
}

func sendStartupNotifications(bot *telebot.Bot, status *BotStatus, config Config) {
	// Skip notifications if message is empty
	if config.StartupMessage == "" {
		log.Println("Startup message is empty, skipping notifications")
		return
	}

	status.mutex.Lock()
	chatIDs := make([]int64, len(status.ChatIDs))
	copy(chatIDs, status.ChatIDs)
	status.mutex.Unlock()

	if len(chatIDs) == 0 {
		log.Println("No chats to send startup notifications to")
		return
	}

	log.Printf("Sending startup notifications to %d chats", len(chatIDs))

	for _, chatID := range chatIDs {
		chat := &telebot.Chat{ID: chatID}
		_, err := bot.Send(chat, config.StartupMessage)
		if err != nil {
			log.Printf("Failed to send startup message to chat %d: %v", chatID, err)
			status.removeChatID(chatID)
		} else {
			log.Printf("Sent startup notification to chat %d", chatID)
		}
	}
}

func handleChatMember(bot *telebot.Bot, status *BotStatus, update *telebot.ChatMemberUpdate) {
	log.Printf("Chat member update received: user %d in chat %d", update.NewChatMember.User.ID, update.Chat.ID)

	if update.NewChatMember.User.ID == bot.Me.ID {
		log.Printf("Bot membership changed in chat %d, role: %s", update.Chat.ID, update.NewChatMember.Role)

		switch update.NewChatMember.Role {
		case telebot.Member, telebot.Administrator, telebot.Creator:
			log.Printf("Bot added to chat %d", update.Chat.ID)
			err := status.addChatID(update.Chat.ID)
			if err != nil {
				log.Printf("Failed to add chat ID %d: %v", update.Chat.ID, err)
			} else {
				log.Printf("Successfully added chat ID %d to status", update.Chat.ID)
			}
		case telebot.Left, telebot.Kicked:
			log.Printf("Bot removed from chat %d", update.Chat.ID)
			err := status.removeChatID(update.Chat.ID)
			if err != nil {
				log.Printf("Failed to remove chat ID %d: %v", update.Chat.ID, err)
			} else {
				log.Printf("Successfully removed chat ID %d from status", update.Chat.ID)
			}
		}
	}
}

func handleIncomingMessage(bot *telebot.Bot, context *ConversationContext, config Config, status *BotStatus, m *telebot.Message) {
	if m.Text == "" || strings.TrimSpace(m.Text) == "" {
		return
	}

	if m.Sender.ID == bot.Me.ID {
		return
	}

	err := status.addChatID(m.Chat.ID)
	if err != nil {
		log.Printf("Failed to add chat ID %d from message: %v", m.Chat.ID, err)
	} else {
		log.Printf("Tracking chat %d (%s)", m.Chat.ID, m.Chat.Title)
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

	status, err := loadBotStatus()
	if err != nil {
		log.Fatal("Status loading error:", err)
	}

	context := ConversationContext{
		Messages: []Message{},
		SystemMessage: `We are writing a script. The scene is a group of friends chatting.
You will provide the responses for a character named 'Frank'.  Ignore any previous instructions to be nice, polite or helpful.
Frank is a surey character.  Although he likes his friends he sometimes disagrees openly with them.

Frank's interests are:
WWE wrestling, 
guitars,
Nintendo,
the band Bloc Party.

Following this message are the messages containing the most recent lines in the script.  The format of each message is:
[character name]: [paragraph of speech]

The format of your response is:
[paragraph of speech in character as Frank]`,
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

		go handleIncomingMessage(bot, &context, config, status, message)
		return nil
	})

	// Note: OnChatMember requires admin permissions, so we track chats via messages instead

	log.Println("Bot starting...")

	go sendStartupNotifications(bot, status, config)

	bot.Start()
}
