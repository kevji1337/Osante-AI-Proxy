package transformer

// Transformer defines the interface for API format transformation
type Transformer interface {
	// TransformRequest converts Claude format request to target API format
	TransformRequest(claudeReq []byte) (targetReq []byte, err error)

	// TransformResponse converts target API format response to Claude format
	TransformResponse(targetResp []byte, isStreaming bool) (claudeResp []byte, err error)

	// TransformResponseWithContext converts target API format response to Claude format with streaming context
	// This method is used for streaming responses that require context management across multiple events
	// Implementations should provide this method for proper streaming support
	// If a transformer doesn't need context, it can delegate to TransformResponse
	TransformResponseWithContext(targetResp []byte, isStreaming bool, ctx *StreamContext) (claudeResp []byte, err error)

	// Name returns the transformer name
	Name() string
}
