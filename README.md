# Telegram LLM Bot

A Go application that runs a Telegram bot which forwards group chat messages to an OpenAI-compatible LLM API and posts responses back to the chat.

## Features

- 10-second message batching with timer reset
- 8000 character context limit with automatic trimming
- Thread-safe message processing
- Support for OpenAI-compatible APIs
- Handles multiple users in group chats
- Automatic response truncation for Telegram limits

## Setup

1. Create a Telegram bot:
   - Message @BotFather on Telegram
   - Use `/newbot` command and follow instructions
   - Copy the bot token

2. Get an OpenAI API key (or compatible service)

3. Configure the bot:
   ```bash
   cp config.json config.json.backup
   # Edit config.json with your actual tokens
   ```

4. Build the application:
   ```bash
   go build -o telegram-llm-bot main.go
   ```

5. Run the bot:
   ```bash
   ./telegram-llm-bot
   ```

## Configuration

Edit `config.json` with your actual values:

```json
{
  "telegram_token": "YOUR_TELEGRAM_BOT_TOKEN",
  "openai_api_key": "YOUR_API_KEY", 
  "openai_api_url": "https://api.openai.com/v1/chat/completions",
  "openai_model": "gpt-3.5-turbo"
}
```

### Configuration Fields

- `telegram_token`: Your Telegram bot token from @BotFather
- `openai_api_key`: Your OpenAI API key or compatible service key
- `openai_api_url`: API endpoint URL (default works for OpenAI)
- `openai_model`: Model name to use (e.g., "gpt-3.5-turbo", "gpt-4")

## Usage

1. Add the bot to a Telegram group
2. Grant it permission to read messages
3. Start chatting - the bot will respond to conversations after a 10-second batch delay

## How It Works

1. Bot receives messages from users in the group
2. Messages are batched for 10 seconds (timer resets with each new message)
3. After 10 seconds of no new messages, the batch is sent to the LLM
4. The LLM response is posted back to the group
5. All messages are stored in context for future requests (up to 8000 characters)

## Important Notes

- The bot will lose conversation context when restarted
- Only works in one group chat at a time
- Bot ignores its own messages to prevent loops
- Responses are truncated to 4096 characters (Telegram limit)
- Oldest messages are automatically removed when context exceeds 8000 characters

## Troubleshooting

- **Bot not responding**: Check that bot token is correct and bot is added to group
- **API errors**: Verify OpenAI API key and endpoint URL
- **Permission errors**: Ensure bot has read/write permissions in the group
- **Build errors**: Run `go mod tidy` to fix dependency issues