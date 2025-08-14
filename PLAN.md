# Telegram LLM Bot Implementation Specification

## Project Overview

Create a Go application that runs a Telegram bot which forwards group chat messages to an OpenAI-compatible LLM API and posts responses back to the chat.

## Key Requirements

- Language: Go
- Telegram library: telebot v3
- Runs on Linux server
- Single group chat support
- 10-second message batching
- 8000 character context limit
- Configurable via JSON file

## Project Structure

```
telegram-llm-bot/
├── main.go
├── config.json
├── go.mod
├── go.sum
└── README.md
```

## Step-by-Step Implementation Plan

### Step 1: Initialize Go Module

1. Create project directory `telegram-llm-bot`
1. Run `go mod init telegram-llm-bot`
1. Add dependencies:
- `go get gopkg.in/telebot.v3`
- `go get github.com/go-resty/resty/v2` (for HTTP requests to OpenAI API)

### Step 2: Create Configuration Structure

Create a `Config` struct with fields:

- `TelegramToken` (string) - maps to JSON “telegram_token”
- `OpenAIAPIKey` (string) - maps to JSON “openai_api_key”
- `OpenAIAPIURL` (string) - maps to JSON “openai_api_url”
- `OpenAIModel` (string) - maps to JSON “openai_model”

Create a function `loadConfig()` that:

1. Opens “config.json” from the same directory as the executable
1. Uses `json.Unmarshal` to parse into Config struct
1. Returns error if file not found or invalid JSON
1. Validates all fields are non-empty

Example config.json:

```json
{
  "telegram_token": "YOUR_TELEGRAM_BOT_TOKEN",
  "openai_api_key": "YOUR_API_KEY",
  "openai_api_url": "https://api.openai.com/v1/chat/completions",
  "openai_model": "gpt-3.5-turbo"
}
```

### Step 3: Define Data Structures

Create a `Message` struct:

- `Username` (string)
- `Text` (string)
- `Timestamp` (time.Time)
- `IsBot` (bool) - true if message is from the bot

Create a `ConversationContext` struct:

- `Messages` ([]Message) - stores conversation history
- `SystemMessage` (string) - the system prompt
- `PendingMessages` ([]Message) - messages waiting to be sent
- `LastMessageTime` (time.Time) - for batch timing
- `Timer` (*time.Timer) - for the 10-second delay
- `Mutex` (sync.Mutex) - for thread safety

### Step 4: Implement OpenAI API Integration

Create OpenAI request/response structs matching their API:

```go
type OpenAIRequest struct {
    Model    string          `json:"model"`
    Messages []OpenAIMessage `json:"messages"`
}

type OpenAIMessage struct {
    Role    string `json:"role"`    // "system", "user", or "assistant"
    Content string `json:"content"`
}

type OpenAIResponse struct {
    Choices []struct {
        Message OpenAIMessage `json:"message"`
    } `json:"choices"`
}
```

Create function `callOpenAI(config Config, messages []OpenAIMessage) (string, error)`:

1. Use resty client to POST to config.OpenAIAPIURL
1. Set Authorization header: “Bearer “ + config.OpenAIAPIKey
1. Set Content-Type: “application/json”
1. Marshal OpenAIRequest with config.OpenAIModel and messages
1. Parse response, extract choices[0].message.content
1. Return error if request fails or response invalid

### Step 5: Implement Context Management

Create function `formatMessagesForContext(context *ConversationContext) []OpenAIMessage`:

1. Start with system message as first OpenAIMessage with role=“system”
1. Iterate through context.Messages:
- If IsBot is false: create OpenAIMessage with role=“user”, content=“Username: text”
- If IsBot is true: create OpenAIMessage with role=“assistant”, content=text (no username prefix)
1. Add all pending messages as role=“user” with “Username: text” format
1. Return the array

Create function `trimContext(context *ConversationContext, maxChars int)`:

1. Calculate total character count of all messages:
- For user messages: count “Username: text”
- For bot messages: count just the text
1. While total > maxChars:
- Remove oldest message from context.Messages
- Recalculate total
1. Never remove the system message (it’s separate from Messages slice)

Create function `addToContext(context *ConversationContext, username string, text string, isBot bool)`:

1. Create new Message with username, text, current timestamp, and isBot flag
1. Append to context.Messages
1. Call trimContext with 8000 character limit

### Step 6: Implement Message Batching Logic

Create function `handleIncomingMessage(bot *telebot.Bot, context *ConversationContext, config Config, m *telebot.Message)`:

1. Check if message text is empty/whitespace - if so, return
1. Check if message is from the bot itself - if so, return
1. Lock context.Mutex
1. Create Message from m.Sender.Username (or FirstName+LastName if username empty) and m.Text
1. Add to context.PendingMessages
1. If context.Timer is not nil, stop it
1. Create new timer for 10 seconds that calls `processBatch` when fired
1. Store timer reference in context.Timer
1. Unlock context.Mutex

Create function `processBatch(bot *telebot.Bot, chat *telebot.Chat, context *ConversationContext, config Config)`:

1. Lock context.Mutex
1. If no pending messages, unlock and return
1. Add all pending messages to context.Messages (with IsBot=false)
1. Format all messages for OpenAI using formatMessagesForContext
1. Clear pending messages
1. Set context.Timer to nil
1. Unlock context.Mutex (before API call)
1. Call OpenAI API
1. If error, log to stdout with `log.Printf("OpenAI API error: %v", err)` and return
1. If success:
- Truncate response to 4096 characters if needed
- Send response to Telegram chat using bot.Send(chat, truncatedResponse)
- Lock context.Mutex
- Add bot’s response to context with addToContext (isBot=true)
- Unlock context.Mutex

### Step 7: Implement Startup Message Loading

Create function `loadRecentMessages(bot *telebot.Bot, chat *telebot.Chat, context *ConversationContext)`:

1. Note: telebot v3 doesn’t have a built-in way to fetch message history
1. Since we can’t reliably get past messages with telebot v3, skip this feature
1. Instead, just log: `log.Println("Bot started, waiting for new messages...")`
1. (Alternative: mention in comments that this would require using raw Telegram API)

### Step 8: Implement Main Function

Main function flow:

1. Load configuration using loadConfig()
- If error, log.Fatal() and exit
1. Initialize ConversationContext with:
- Empty Messages slice
- SystemMessage: “You are a helpful assistant participating in a group chat. Be concise and friendly.”
- Empty PendingMessages
- Nil Timer
- Initialize Mutex
1. Create telebot bot:
   
   ```go
   pref := telebot.Settings{
       Token:  config.TelegramToken,
       Poller: &telebot.LongPoller{Timeout: 10 * time.Second},
   }
   bot, err := telebot.NewBot(pref)
   ```
1. Set up message handler:
   
   ```go
   bot.Handle(telebot.OnText, func(c telebot.Context) error {
       message := c.Message()
       chat := c.Chat()
       
       // Skip if from bot itself
       if message.Sender.ID == bot.Me.ID {
           return nil
       }
       
       // Handle the message
       go handleIncomingMessage(bot, &context, config, message)
       return nil
   })
   ```
1. Log startup: `log.Println("Bot starting...")`
1. Start bot with bot.Start() - this blocks forever

### Step 9: Error Handling Requirements

All errors should be logged to stdout with descriptive messages:

- Config loading errors: Fatal (exit program)
- OpenAI API errors: Log and continue
- Telegram API errors: Log and continue
- JSON parsing errors: Log and continue

Use format: `log.Printf("Error type: %v", err)`

### Step 10: Build Instructions

Create README.md with:

```markdown
# Telegram LLM Bot

## Setup
1. Create config.json with your API keys
2. Build: `go build -o telegram-llm-bot main.go`
3. Run: `./telegram-llm-bot`

## Configuration
See config.json example above
```

## Important Implementation Notes

1. **Character Counting**: When counting characters for the 8000 limit, count the formatted message:
- User messages: “Username: message text”
- Bot messages: just the message text
1. **Bot Detection**: The bot must not respond to its own messages - check if message.Sender.ID == bot.Me.ID
1. **Username Handling**: Some users don’t have usernames:
   
   ```go
   username := m.Sender.Username
   if username == "" {
       username = m.Sender.FirstName
       if m.Sender.LastName != "" {
           username += " " + m.Sender.LastName
       }
   }
   ```
1. **Thread Safety**: Since telebot v3 handles messages concurrently, always lock mutex when accessing context
1. **Timer Management**: Always cancel existing timer before creating new one:
   
   ```go
   if context.Timer != nil {
       context.Timer.Stop()
   }
   ```
1. **Message Batching**: All pending messages should be moved to context.Messages before sending to OpenAI
1. **Empty Messages**: Check for nil, empty string, and whitespace-only messages:
   
   ```go
   if m.Text == "" || strings.TrimSpace(m.Text) == "" {
       return
   }
   ```
1. **Message Truncation**: If response > 4096 characters:
   
   ```go
   if len(response) > 4096 {
       response = response[:4096]
   }
   ```

## Testing Checklist

The implementation should handle:

- Multiple rapid messages (proper batching with 10-second delay resetting)
- Long messages from OpenAI (truncation to 4096 chars)
- Missing usernames from Telegram users (use FirstName+LastName)
- API failures (error logging without crashing)
- Context exceeding 8000 characters (oldest messages trimmed)
- Empty/whitespace messages (ignored)
- Bot’s own messages (ignored, not processed)
- Concurrent message handling (mutex protection)

## Expected Behavior Flow

1. Bot receives message from user “Alice”: “Hello everyone”
1. 10-second timer starts
1. Bot receives message from user “Bob”: “Hi Alice!” (5 seconds later)
1. Timer resets to 10 seconds
1. No messages for 10 seconds
1. Bot sends both messages to OpenAI as:
- System: “You are a helpful assistant participating in a group chat. Be concise and friendly.”
- User: “Alice: Hello everyone”
- User: “Bob: Hi Alice!”
1. OpenAI responds with “Hello Alice and Bob! How can I help you today?”
1. Bot posts this response to Telegram
1. Bot adds all three messages to context for future requests

## Final Notes

- The bot will lose all conversation context when restarted
- The bot only works in one group chat at a time
- The system message is always kept and never trimmed
- All responses from the bot should appear as regular messages in the chat
- The bot should be responsive and batch messages intelligently to avoid overwhelming the LLM with individual messages