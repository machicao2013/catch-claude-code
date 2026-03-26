package recorder

import "encoding/json"

type Record struct {
	ID         string       `json:"id"`
	Timestamp  string       `json:"timestamp"`
	DurationMs int64        `json:"duration_ms"`
	Request    RequestData  `json:"request"`
	Response   ResponseData `json:"response"`
}

type RequestData struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
}

type ResponseData struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
}

type Recorder interface {
	Write(record Record) error
	Close() error
	FilePath() string
}
