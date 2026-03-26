package proxy

import (
	"strings"
	"testing"
)

func TestParseSSEEvents(t *testing.T) {
	raw := `event: message_start
data: {"type":"message_start","message":{"id":"msg_01","type":"message","role":"assistant","model":"claude-4.6-opus","content":[],"usage":{"input_tokens":100}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":50}}

event: message_stop
data: {"type":"message_stop"}

`

	events := ParseSSEEvents(strings.NewReader(raw))

	if len(events) != 7 {
		t.Fatalf("expected 7 events, got %d", len(events))
	}
	if events[0].Event != "message_start" {
		t.Errorf("event[0].Event = %q, want message_start", events[0].Event)
	}
	if events[6].Event != "message_stop" {
		t.Errorf("event[6].Event = %q, want message_stop", events[6].Event)
	}
}

func TestReassembleSSEResponse(t *testing.T) {
	raw := `event: message_start
data: {"type":"message_start","message":{"id":"msg_01","type":"message","role":"assistant","model":"claude-4.6-opus","content":[],"stop_reason":null,"usage":{"input_tokens":100,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":50}}

event: message_stop
data: {"type":"message_stop"}

`

	msg, err := ReassembleSSEResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ReassembleSSEResponse: %v", err)
	}

	if msg.ID != "msg_01" {
		t.Errorf("ID = %q, want msg_01", msg.ID)
	}
	if msg.Model != "claude-4.6-opus" {
		t.Errorf("Model = %q, want claude-4.6-opus", msg.Model)
	}
	if msg.StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want end_turn", msg.StopReason)
	}
	if msg.Usage.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", msg.Usage.InputTokens)
	}
	if msg.Usage.OutputTokens != 50 {
		t.Errorf("OutputTokens = %d, want 50", msg.Usage.OutputTokens)
	}
	if len(msg.Content) != 1 {
		t.Fatalf("len(Content) = %d, want 1", len(msg.Content))
	}
	if msg.Content[0].Type != "text" {
		t.Errorf("Content[0].Type = %q, want text", msg.Content[0].Type)
	}
	if msg.Content[0].Text != "Hello world" {
		t.Errorf("Content[0].Text = %q, want 'Hello world'", msg.Content[0].Text)
	}
}

func TestReassembleSSE_ToolUse(t *testing.T) {
	raw := `event: message_start
data: {"type":"message_start","message":{"id":"msg_02","type":"message","role":"assistant","model":"claude-4.6-opus","content":[],"stop_reason":null,"usage":{"input_tokens":200,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool_01","name":"Bash","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"command\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"ls\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":30}}

event: message_stop
data: {"type":"message_stop"}

`

	msg, err := ReassembleSSEResponse([]byte(raw))
	if err != nil {
		t.Fatalf("ReassembleSSEResponse: %v", err)
	}
	if msg.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want tool_use", msg.StopReason)
	}
	if len(msg.Content) != 1 {
		t.Fatalf("len(Content) = %d, want 1", len(msg.Content))
	}
	if msg.Content[0].Type != "tool_use" {
		t.Errorf("Content[0].Type = %q, want tool_use", msg.Content[0].Type)
	}
	if msg.Content[0].Name != "Bash" {
		t.Errorf("Content[0].Name = %q, want Bash", msg.Content[0].Name)
	}
}
