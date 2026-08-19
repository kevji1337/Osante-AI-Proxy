package chat

import (
	"strings"

	"github.com/kevji1337/Osante-AI-Proxy/internal/transformer"
	"github.com/kevji1337/Osante-AI-Proxy/internal/transformer/convert"
)

// OneMinAITransformer transforms OpenAI Chat Completions requests to 1min.AI format.
type OneMinAITransformer struct {
	model         string
	stream        bool
	originalModel string
}

// NewOneMinAITransformer creates a new 1min.AI transformer for OpenAI Chat clients.
func NewOneMinAITransformer(model string) *OneMinAITransformer {
	return &OneMinAITransformer{model: model}
}

func (t *OneMinAITransformer) Name() string {
	return "cx_chat_1minai"
}

func (t *OneMinAITransformer) TransformRequest(req []byte) ([]byte, error) {
	out, stream, origModel, err := convert.OpenAIReqToOneMinAI(req, t.model)
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
		return convert.OneMinAIRespToOpenAISSE(resp, model)
	}
	return convert.OneMinAIRespToOpenAI(resp, model)
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
