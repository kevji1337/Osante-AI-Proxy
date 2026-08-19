package cc

import (
	"strings"

	"github.com/kevji1337/Osante-AI-Proxy/internal/transformer"
	"github.com/kevji1337/Osante-AI-Proxy/internal/transformer/convert"
)

// OneMinAITransformer transforms Claude Code requests to 1min.AI CODE_GENERATOR format.
type OneMinAITransformer struct {
	model         string
	stream        bool
	originalModel string
}

// NewOneMinAITransformer creates a new 1min.AI transformer for Claude Code clients.
func NewOneMinAITransformer(model string) *OneMinAITransformer {
	return &OneMinAITransformer{model: model}
}

func (t *OneMinAITransformer) Name() string {
	return "cc_1minai"
}

func (t *OneMinAITransformer) TransformRequest(req []byte) ([]byte, error) {
	out, stream, origModel, err := convert.ClaudeReqToOneMinAI(req, t.model)
	if err != nil {
		return nil, err
	}
	t.stream = stream
	t.originalModel = origModel
	return out, nil
}

func (t *OneMinAITransformer) TransformResponse(resp []byte, isStreaming bool) ([]byte, error) {
	model := t.echoModel()
	if t.stream {
		return convert.OneMinAIRespToClaudeSSE(resp, model)
	}
	return convert.OneMinAIRespToClaude(resp, model)
}

func (t *OneMinAITransformer) TransformResponseWithContext(resp []byte, isStreaming bool, ctx *transformer.StreamContext) ([]byte, error) {
	return t.TransformResponse(resp, isStreaming)
}

func (t *OneMinAITransformer) echoModel() string {
	if strings.TrimSpace(t.model) != "" {
		return t.model
	}
	if strings.TrimSpace(t.originalModel) != "" {
		return t.originalModel
	}
	return "1minai"
}
