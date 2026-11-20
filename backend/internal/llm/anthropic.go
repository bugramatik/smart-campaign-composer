package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type AnthropicConfig struct {
	APIKey string
	Model  string
	// Effort controls thinking depth; Opus 5.5 thinks adaptively and is
	// steered with effort rather than a thinking budget.
	Effort string
	// Fallbacks enables server-side fallback: if the primary model declines,
	// the API re-serves the request with a fallback model in the same call.
	Fallbacks bool
	// BaseURL overrides the API endpoint (tests point it at an httptest server).
	BaseURL string
}

type Anthropic struct {
	client anthropic.Client
	cfg    AnthropicConfig
}

func NewAnthropic(cfg AnthropicConfig) *Anthropic {
	opts := []option.RequestOption{option.WithAPIKey(cfg.APIKey), option.WithMaxRetries(2)}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	if cfg.Effort == "" {
		cfg.Effort = "medium"
	}
	return &Anthropic{client: anthropic.NewClient(opts...), cfg: cfg}
}

func (a *Anthropic) Provider() string { return "anthropic" }
func (a *Anthropic) Model() string    { return a.cfg.Model }

func (a *Anthropic) Stream(ctx context.Context, req Request, h StreamHandler) (Response, error) {
	schema, err := json.Marshal(req.Schema)
	if err != nil {
		return Response{}, fmt.Errorf("marshal schema: %w", err)
	}
	msgs := make([]anthropic.BetaMessageParam, 0, len(req.Messages))
	for _, m := range req.Messages {
		block := anthropic.NewBetaTextBlock(m.Text)
		if m.Role == RoleAssistant {
			msgs = append(msgs, anthropic.BetaMessageParam{Role: anthropic.BetaMessageParamRoleAssistant, Content: []anthropic.BetaContentBlockParamUnion{block}})
		} else {
			msgs = append(msgs, anthropic.NewBetaUserMessage(block))
		}
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 16000
	}

	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(a.cfg.Model),
		MaxTokens: int64(maxTokens),
		// The system prompt is identical across calls of a purpose, so it is
		// cached; per-campaign data lives in the user turn.
		System: []anthropic.BetaTextBlockParam{{
			Text:         req.System,
			CacheControl: anthropic.NewBetaCacheControlEphemeralParam(),
		}},
		Messages: msgs,
		OutputConfig: anthropic.BetaOutputConfigParam{
			Effort: anthropic.BetaOutputConfigEffort(a.cfg.Effort),
			Format: anthropic.BetaJSONOutputFormatParam{Schema: json.RawMessage(schema)},
		},
	}
	if a.cfg.Fallbacks {
		params.Fallbacks = anthropic.BetaFallbacksParamUnion{OfDefault: "default"}
		params.Betas = append(params.Betas, anthropic.AnthropicBetaServerSideFallback2026_07_01)
	}

	stream := a.client.Beta.Messages.NewStreaming(ctx, params)
	defer stream.Close()

	var (
		msg  anthropic.BetaMessage
		text strings.Builder
	)
	for stream.Next() {
		ev := stream.Current()
		if err := msg.Accumulate(ev); err != nil {
			return Response{}, fmt.Errorf("accumulate stream: %w", err)
		}
		switch ev.Type {
		case "content_block_start":
			// A fallback block marks the switch to the fallback model; text
			// produced before it belongs to the declined attempt.
			if ev.ContentBlock.Type == "fallback" && text.Len() > 0 {
				text.Reset()
				h.reset()
			}
		case "content_block_delta":
			if ev.Delta.Type == "text_delta" {
				text.WriteString(ev.Delta.Text)
				h.delta(ev.Delta.Text)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return Response{}, fmt.Errorf("anthropic stream: %w", err)
	}

	resp := Response{
		Text:         text.String(),
		StopReason:   string(msg.StopReason),
		Model:        string(msg.Model),
		InputTokens:  int(msg.Usage.InputTokens + msg.Usage.CacheReadInputTokens + msg.Usage.CacheCreationInputTokens),
		OutputTokens: int(msg.Usage.OutputTokens),
	}
	switch msg.StopReason {
	case anthropic.BetaStopReasonRefusal:
		return resp, &RefusalError{Category: string(msg.StopDetails.Category), Explanation: msg.StopDetails.Explanation}
	case anthropic.BetaStopReasonMaxTokens:
		return resp, ErrTruncated
	}
	return resp, nil
}
