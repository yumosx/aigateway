package eval

import (
	"testing"

	"github.com/aegisflow/aegisflow/pkg/types"
)

func TestScoreEmptyResponse(t *testing.T) {
	// No choices at all
	resp := &types.ChatCompletionResponse{}
	result := ScoreResponse(resp, 100, 50, 10, 2.0)
	if result.Score != 0 {
		t.Errorf("expected score 0 for empty choices, got %d", result.Score)
	}
	if len(result.Issues) != 1 || result.Issues[0] != "empty response" {
		t.Errorf("expected 'empty response' issue, got %v", result.Issues)
	}

	// One choice but empty content
	resp2 := &types.ChatCompletionResponse{
		Choices: []types.Choice{{Message: types.Message{Content: ""}}},
	}
	result2 := ScoreResponse(resp2, 100, 50, 10, 2.0)
	if result2.Score != 0 {
		t.Errorf("expected score 0 for empty content, got %d", result2.Score)
	}
}

func TestScoreHealthyResponse(t *testing.T) {
	resp := &types.ChatCompletionResponse{
		Choices: []types.Choice{
			{
				Message:      types.Message{Content: "Hello, world!"},
				FinishReason: "stop",
			},
		},
		Usage: types.Usage{
			PromptTokens:     50,
			CompletionTokens: 20,
		},
	}
	result := ScoreResponse(resp, 100, 200, 10, 2.0)
	if result.Score != 100 {
		t.Errorf("expected score 100, got %d", result.Score)
	}
	if len(result.Issues) != 0 {
		t.Errorf("expected no issues, got %v", result.Issues)
	}
}

func TestScoreTruncatedResponse(t *testing.T) {
	resp := &types.ChatCompletionResponse{
		Choices: []types.Choice{
			{
				Message:      types.Message{Content: "partial output"},
				FinishReason: "length",
			},
		},
		Usage: types.Usage{
			PromptTokens:     50,
			CompletionTokens: 20,
		},
	}
	result := ScoreResponse(resp, 100, 200, 10, 2.0)
	if result.Score != 70 {
		t.Errorf("expected score 70, got %d", result.Score)
	}
}

func TestScoreShortResponse(t *testing.T) {
	resp := &types.ChatCompletionResponse{
		Choices: []types.Choice{
			{
				Message:      types.Message{Content: "ok"},
				FinishReason: "stop",
			},
		},
		Usage: types.Usage{
			PromptTokens:     50,
			CompletionTokens: 3,
		},
	}
	result := ScoreResponse(resp, 100, 200, 10, 2.0)
	if result.Score != 80 {
		t.Errorf("expected score 80, got %d", result.Score)
	}
}

func TestScoreLatencyDegradation(t *testing.T) {
	resp := &types.ChatCompletionResponse{
		Choices: []types.Choice{
			{
				Message:      types.Message{Content: "Hello!"},
				FinishReason: "stop",
			},
		},
		Usage: types.Usage{
			PromptTokens:     50,
			CompletionTokens: 20,
		},
	}
	// latency 500ms, p50 is 100ms, multiplier 2.0 → 500 > 200, so -10
	result := ScoreResponse(resp, 500, 100, 10, 2.0)
	if result.Score != 90 {
		t.Errorf("expected score 90, got %d", result.Score)
	}
}

func TestScoreCumulativeDeductions(t *testing.T) {
	resp := &types.ChatCompletionResponse{
		Choices: []types.Choice{
			{
				Message:      types.Message{Content: "short"},
				FinishReason: "length",
			},
		},
		Usage: types.Usage{
			PromptTokens:     50,
			CompletionTokens: 3,
		},
	}
	// truncated (-30) + short (-20) = 50
	result := ScoreResponse(resp, 100, 200, 10, 2.0)
	if result.Score != 50 {
		t.Errorf("expected score 50, got %d", result.Score)
	}
}
