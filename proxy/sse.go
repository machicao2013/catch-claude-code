package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type SSEEvent struct {
	Event string
	Data  string
}

type AssembledMessage struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Model      string         `json:"model"`
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      AssembledUsage `json:"usage"`
}

type ContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
}

type AssembledUsage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens,omitempty"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens,omitempty"`
}

func ParseSSEEvents(r io.Reader) []SSEEvent {
	var events []SSEEvent
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	var currentEvent, currentData string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if currentData != "" {
				events = append(events, SSEEvent{Event: currentEvent, Data: currentData})
			}
			currentEvent = ""
			currentData = ""
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			currentData = strings.TrimPrefix(line, "data: ")
		}
	}
	return events
}

func ReassembleSSEResponse(raw []byte) (*AssembledMessage, error) {
	events := ParseSSEEvents(strings.NewReader(string(raw)))

	var msg AssembledMessage
	type blockBuilder struct {
		typ      string
		text     strings.Builder
		thinking strings.Builder
		id       string
		name     string
		inputJSON strings.Builder
	}
	blocks := make(map[int]*blockBuilder)

	for _, ev := range events {
		switch ev.Event {
		case "message_start":
			var wrapper struct {
				Message struct {
					ID    string `json:"id"`
					Type  string `json:"type"`
					Role  string `json:"role"`
					Model string `json:"model"`
					Usage struct {
						// Anthropic 格式
						InputTokens         int64 `json:"input_tokens"`
						CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
						CacheReadTokens     int64 `json:"cache_read_input_tokens"`
						// OpenAI / GLM 兼容格式
						PromptTokens int64 `json:"prompt_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			json.Unmarshal([]byte(ev.Data), &wrapper)
			msg.ID = wrapper.Message.ID
			msg.Type = wrapper.Message.Type
			msg.Role = wrapper.Message.Role
			msg.Model = wrapper.Message.Model
			msg.Usage.InputTokens = wrapper.Message.Usage.InputTokens
			if msg.Usage.InputTokens == 0 {
				msg.Usage.InputTokens = wrapper.Message.Usage.PromptTokens
			}
			msg.Usage.CacheCreationTokens = wrapper.Message.Usage.CacheCreationTokens
			msg.Usage.CacheReadTokens = wrapper.Message.Usage.CacheReadTokens

		case "content_block_start":
			var cb struct {
				Index        int `json:"index"`
				ContentBlock struct {
					Type string `json:"type"`
					Text string `json:"text"`
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"content_block"`
			}
			json.Unmarshal([]byte(ev.Data), &cb)
			b := &blockBuilder{typ: cb.ContentBlock.Type, id: cb.ContentBlock.ID, name: cb.ContentBlock.Name}
			b.text.WriteString(cb.ContentBlock.Text)
			blocks[cb.Index] = b

		case "content_block_delta":
			var delta struct {
				Index int `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					Thinking    string `json:"thinking"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			json.Unmarshal([]byte(ev.Data), &delta)
			if b, ok := blocks[delta.Index]; ok {
				if delta.Delta.Type == "text_delta" {
					b.text.WriteString(delta.Delta.Text)
				} else if delta.Delta.Type == "thinking_delta" {
					b.thinking.WriteString(delta.Delta.Thinking)
				} else if delta.Delta.Type == "input_json_delta" {
					b.inputJSON.WriteString(delta.Delta.PartialJSON)
				}
			}

		case "message_delta":
			var md struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage struct {
					// Anthropic 格式
					OutputTokens int64 `json:"output_tokens"`
					// OpenAI / GLM 兼容格式
					CompletionTokens int64 `json:"completion_tokens"`
				} `json:"usage"`
			}
			json.Unmarshal([]byte(ev.Data), &md)
			msg.StopReason = md.Delta.StopReason
			msg.Usage.OutputTokens = md.Usage.OutputTokens
			if msg.Usage.OutputTokens == 0 {
				msg.Usage.OutputTokens = md.Usage.CompletionTokens
			}
		}
	}

	for i := 0; i < len(blocks); i++ {
		b, ok := blocks[i]
		if !ok {
			return nil, fmt.Errorf("missing content block at index %d", i)
		}
		cb := ContentBlock{Type: b.typ}
		switch b.typ {
		case "text":
			cb.Text = b.text.String()
		case "thinking":
			cb.Thinking = b.thinking.String()
		case "tool_use":
			cb.ID = b.id
			cb.Name = b.name
			inputStr := b.inputJSON.String()
			if inputStr != "" {
				cb.Input = json.RawMessage(inputStr)
			} else {
				cb.Input = json.RawMessage("{}")
			}
		}
		msg.Content = append(msg.Content, cb)
	}

	return &msg, nil
}
