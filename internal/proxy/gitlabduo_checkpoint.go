package proxy

// Checkpoint parsing for the GitLab Duo Workflow protocol.
//
// The WebSocket transport is JSON in both directions (the server sometimes
// uses a binary WebSocket opcode but the payload is still UTF-8 JSON). The
// `checkpoint` field inside a newCheckpoint message is itself a JSON blob whose
// channel_values.ui_chat_log holds the chat history; the last assistant entry
// is the answer we surface to Claude Code.

import (
	"encoding/json"
)

// uiChatLogEntry mirrors the GitLab Duo ui_chat_log schema (from duo.exe):
//
//	message_type:     "user" | "agent" | "request" | "tool"
//	message_sub_type: null | "tool_use" | "tool_result" | ... (intermediate steps)
//	content:          string
//	status:           string | null  ("failure" on errors, "success" otherwise)
type uiChatLogEntry struct {
	MessageType    string `json:"message_type"`
	MessageSubType string `json:"message_sub_type"`
	Content        string `json:"content"`
	Status         string `json:"status"`
}

// extractAnswerFromCheckpoint parses the checkpoint JSON blob and extracts the
// last *final* agent message from channel_values.ui_chat_log.
//
// Filtering rules:
//   - message_type must be "agent" (skip user/request/tool entries).
//   - message_sub_type must be empty/null. Sub-typed entries are intermediate
//     steps (tool_use, tool_result, plan_update, …) and would leak internal
//     reasoning into the chat reply if surfaced.
//   - status != "failure" is preferred; on no success we fall back to the
//     latest failure so the user sees GitLab's error text.
func extractAnswerFromCheckpoint(checkpointJSON string) string {
	if checkpointJSON == "" {
		return ""
	}
	var cp struct {
		ChannelValues struct {
			UIChatLog []uiChatLogEntry `json:"ui_chat_log"`
		} `json:"channel_values"`
	}
	if err := json.Unmarshal([]byte(checkpointJSON), &cp); err != nil {
		return ""
	}
	log := cp.ChannelValues.UIChatLog

	isFinalAgent := func(e uiChatLogEntry) bool {
		return e.MessageType == "agent" && e.MessageSubType == "" && e.Content != ""
	}

	// Prefer the last successful, non-sub-typed agent message.
	for i := len(log) - 1; i >= 0; i-- {
		e := log[i]
		if isFinalAgent(e) && e.Status != "failure" {
			return e.Content
		}
	}
	// Fall back to any final agent message (including failures).
	for i := len(log) - 1; i >= 0; i-- {
		e := log[i]
		if isFinalAgent(e) {
			return e.Content
		}
	}
	// Last resort: any agent entry (even sub-typed) so user sees something.
	for i := len(log) - 1; i >= 0; i-- {
		e := log[i]
		if e.MessageType == "agent" && e.Content != "" {
			return e.Content
		}
	}
	return ""
}
