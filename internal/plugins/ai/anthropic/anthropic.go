package anthropic

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/danielmiessler/fabric/internal/chat"
	"github.com/danielmiessler/fabric/internal/domain"
	"github.com/danielmiessler/fabric/internal/plugins"
	"github.com/danielmiessler/fabric/internal/util"
)

const defaultBaseUrl = "https://api.anthropic.com/"

const webSearchToolName = "web_search"
const webSearchToolType = "web_search_20250305"
const sourcesHeader = "## Sources"

const authTokenIdentifier = "claude"

func NewClient() (ret *Client) {
	vendorName := "Anthropic"
	ret = &Client{}

	ret.PluginBase = &plugins.PluginBase{
		Name:            vendorName,
		EnvNamePrefix:   plugins.BuildEnvVariablePrefix(vendorName),
		ConfigureCustom: ret.configure,
	}

	ret.ApiBaseURL = ret.AddSetupQuestion("API Base URL", false)
	ret.ApiBaseURL.Value = defaultBaseUrl
	ret.UseOAuth = ret.AddSetupQuestionBool("Use OAuth login", false)
	ret.ApiKey = ret.PluginBase.AddSetupQuestion("API key", false)

	ret.maxTokens = 4096
	ret.defaultRequiredUserMessage = "Hi"
	ret.models = []string{
		string(anthropic.ModelClaude3_7SonnetLatest), string(anthropic.ModelClaude3_7Sonnet20250219),
		string(anthropic.ModelClaude3_5HaikuLatest), string(anthropic.ModelClaude3_5Haiku20241022),
		string(anthropic.ModelClaude3_5SonnetLatest), string(anthropic.ModelClaude3_5Sonnet20241022),
		string(anthropic.ModelClaude_3_5_Sonnet_20240620), string(anthropic.ModelClaude3OpusLatest),
		string(anthropic.ModelClaude_3_Opus_20240229), string(anthropic.ModelClaude_3_Haiku_20240307),
		string(anthropic.ModelClaudeOpus4_20250514), string(anthropic.ModelClaudeSonnet4_20250514),
	}

	return
}

// IsConfigured returns true if either the API key or OAuth is configured
func (an *Client) IsConfigured() bool {
	// Check if API key is configured
	if an.ApiKey.Value != "" {
		return true
	}

	// Check if OAuth is enabled and has a valid token
	if plugins.ParseBoolElseFalse(an.UseOAuth.Value) {
		storage, err := util.NewOAuthStorage()
		if err != nil {
			return false
		}

		// If no valid token exists, automatically run OAuth flow
		if !storage.HasValidToken(authTokenIdentifier, 5) {
			fmt.Println("OAuth enabled but no valid token found. Starting authentication...")
			_, err := RunOAuthFlow(authTokenIdentifier)
			if err != nil {
				fmt.Printf("OAuth authentication failed: %v\n", err)
				return false
			}
			// After successful OAuth flow, check again
			return storage.HasValidToken(authTokenIdentifier, 5)
		}

		return true
	}

	return false
}

type Client struct {
	*plugins.PluginBase
	ApiBaseURL *plugins.SetupQuestion
	ApiKey     *plugins.SetupQuestion
	UseOAuth   *plugins.SetupQuestion

	maxTokens                  int
	defaultRequiredUserMessage string
	models                     []string

	client anthropic.Client
}

func (an *Client) Setup() (err error) {
	if err = an.PluginBase.Ask(an.Name); err != nil {
		return
	}

	if plugins.ParseBoolElseFalse(an.UseOAuth.Value) {
		// Check if we have a valid stored token
		storage, err := util.NewOAuthStorage()
		if err != nil {
			return err
		}

		if !storage.HasValidToken(authTokenIdentifier, 5) {
			// No valid token, run OAuth flow
			if _, err = RunOAuthFlow(authTokenIdentifier); err != nil {
				return err
			}
		}
	}

	err = an.configure()
	return
}

func (an *Client) configure() (err error) {
	opts := []option.RequestOption{}

	if an.ApiBaseURL.Value != "" {
		opts = append(opts, option.WithBaseURL(an.ApiBaseURL.Value))
	}

	if plugins.ParseBoolElseFalse(an.UseOAuth.Value) {
		// For OAuth, use Bearer token with custom headers
		// Create custom HTTP client that adds OAuth Bearer token and beta header
		baseTransport := &http.Transport{}
		httpClient := &http.Client{
			Transport: NewOAuthTransport(an, baseTransport),
		}
		opts = append(opts, option.WithHTTPClient(httpClient))
	} else {
		opts = append(opts, option.WithAPIKey(an.ApiKey.Value))
	}

	an.client = anthropic.NewClient(opts...)
	return
}

func (an *Client) ListModels() (ret []string, err error) {
	return an.models, nil
}

func (an *Client) SendStream(
	msgs []*chat.ChatCompletionMessage, opts *domain.ChatOptions, channel chan string,
) (err error) {
	messages := an.toMessages(msgs)
	if len(messages) == 0 {
		close(channel)
		// No messages to send after normalization, consider this a non-error condition for streaming.
		return
	}

	ctx := context.Background()

	stream := an.client.Messages.NewStreaming(ctx, an.buildMessageParams(messages, opts))

	for stream.Next() {
		event := stream.Current()

		// directly send any non-empty delta text
		if event.Delta.Text != "" {
			channel <- event.Delta.Text
		}
	}

	if stream.Err() != nil {
		fmt.Printf("Messages stream error: %v\n", stream.Err())
	}
	close(channel)
	return
}

func (an *Client) buildMessageParams(msgs []anthropic.MessageParam, opts *domain.ChatOptions) (
	params anthropic.MessageNewParams) {

	params = anthropic.MessageNewParams{
		Model:       anthropic.Model(opts.Model),
		MaxTokens:   int64(an.maxTokens),
		TopP:        anthropic.Opt(opts.TopP),
		Temperature: anthropic.Opt(opts.Temperature),
		Messages:    msgs,
	}

	// Add Claude Code spoofing system message for OAuth authentication
	if plugins.ParseBoolElseFalse(an.UseOAuth.Value) {
		params.System = []anthropic.TextBlockParam{
			{
				Type: "text",
				Text: "You are Claude Code, Anthropic's official CLI for Claude.",
			},
		}

	}

	if opts.Search {
		// Build the web-search tool definition:
		webTool := anthropic.WebSearchTool20250305Param{
			Name:         webSearchToolName,
			Type:         webSearchToolType,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}

		if opts.SearchLocation != "" {
			webTool.UserLocation.Type = "approximate"
			webTool.UserLocation.Timezone = anthropic.Opt(opts.SearchLocation)
		}

		// Wrap it in the union:
		params.Tools = []anthropic.ToolUnionParam{
			{OfWebSearchTool20250305: &webTool},
		}
	}
	return
}

func (an *Client) Send(ctx context.Context, msgs []*chat.ChatCompletionMessage, opts *domain.ChatOptions) (
	ret string, err error) {

	messages := an.toMessages(msgs)
	if len(messages) == 0 {
		// No messages to send after normalization, return empty string and no error.
		return
	}

	var message *anthropic.Message
	if message, err = an.client.Messages.New(ctx, an.buildMessageParams(messages, opts)); err != nil {
		return
	}

	var textParts []string
	var citations []string
	citationMap := make(map[string]bool) // To avoid duplicate citations

	for _, block := range message.Content {
		if block.Type == "text" && block.Text != "" {
			textParts = append(textParts, block.Text)

			// Extract citations from this text block
			for _, citation := range block.Citations {
				if citation.Type == "web_search_result_location" {
					citationKey := citation.URL + "|" + citation.Title
					if !citationMap[citationKey] {
						citationMap[citationKey] = true
						citationText := fmt.Sprintf("- [%s](%s)", citation.Title, citation.URL)
						if citation.CitedText != "" {
							citationText += fmt.Sprintf(" - \"%s\"", citation.CitedText)
						}
						citations = append(citations, citationText)
					}
				}
			}
		}
	}

	var resultBuilder strings.Builder
	resultBuilder.WriteString(strings.Join(textParts, ""))

	// Append citations if any were found
	if len(citations) > 0 {
		resultBuilder.WriteString("\n\n")
		resultBuilder.WriteString(sourcesHeader)
		resultBuilder.WriteString("\n\n")
		resultBuilder.WriteString(strings.Join(citations, "\n"))
	}
	ret = resultBuilder.String()

	return
}

func (an *Client) toMessages(msgs []*chat.ChatCompletionMessage) (ret []anthropic.MessageParam) {
	// Custom normalization for Anthropic:
	// - System messages become the first part of the first user message.
	// - Messages must alternate user/assistant.
	// - Skip empty messages.

	var anthropicMessages []anthropic.MessageParam
	var systemContent string

	// Note: Claude Code spoofing is now handled in buildMessageParams

	isFirstUserMessage := true
	lastRoleWasUser := false

	for _, msg := range msgs {
		if msg.Content == "" {
			continue // Skip empty messages
		}

		switch msg.Role {
		case chat.ChatMessageRoleSystem:
			// Accumulate system content. It will be prepended to the first user message.
			if systemContent != "" {
				systemContent += "\\n" + msg.Content
			} else {
				systemContent = msg.Content
			}
		case chat.ChatMessageRoleUser:
			userContent := msg.Content
			if isFirstUserMessage && systemContent != "" {
				userContent = systemContent + "\\n\\n" + userContent
				isFirstUserMessage = false // System content now consumed
			}
			if lastRoleWasUser {
				// Enforce alternation: add a minimal assistant message if two user messages are consecutive.
				// This shouldn't happen with current chatter.go logic but is a safeguard.
				anthropicMessages = append(anthropicMessages, anthropic.NewAssistantMessage(anthropic.NewTextBlock("Okay.")))
			}
			anthropicMessages = append(anthropicMessages, anthropic.NewUserMessage(anthropic.NewTextBlock(userContent)))
			lastRoleWasUser = true
		case chat.ChatMessageRoleAssistant:
			// If the first message is an assistant message, and we have system content,
			// prepend a user message with the system content.
			if isFirstUserMessage && systemContent != "" {
				anthropicMessages = append(anthropicMessages, anthropic.NewUserMessage(anthropic.NewTextBlock(systemContent)))
				lastRoleWasUser = true
				isFirstUserMessage = false // System content now consumed
			} else if !lastRoleWasUser && len(anthropicMessages) > 0 {
				// Enforce alternation: add a minimal user message if two assistant messages are consecutive
				// or if an assistant message is first without prior system prompt handling.
				anthropicMessages = append(anthropicMessages, anthropic.NewUserMessage(anthropic.NewTextBlock(an.defaultRequiredUserMessage)))
				lastRoleWasUser = true
			}
			anthropicMessages = append(anthropicMessages, anthropic.NewAssistantMessage(anthropic.NewTextBlock(msg.Content)))
			lastRoleWasUser = false
		default:
			// Other roles (like 'meta') are ignored for Anthropic's message structure.
			continue
		}
	}

	// If only system content was provided, create a user message with it.
	if len(anthropicMessages) == 0 && systemContent != "" {
		anthropicMessages = append(anthropicMessages, anthropic.NewUserMessage(anthropic.NewTextBlock(systemContent)))
	}

	return anthropicMessages
}

func (an *Client) NeedsRawMode(modelName string) bool {
	return false
}
