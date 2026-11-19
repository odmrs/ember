package annasarchive

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func TestDevtoolsUsesPrivatePipeAndMatchesResponses(t *testing.T) {
	reader, writer := io.Pipe()
	d := &devtools{input: writer, replies: make(chan []byte, 3), done: make(chan struct{})}
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	go func() {
		var data []byte
		one := make([]byte, 1)
		for {
			if _, err := reader.Read(one); err != nil {
				return
			}
			if one[0] == 0 {
				break
			}
			data = append(data, one[0])
		}
		var command struct {
			ID                int
			Method, SessionID string
		}
		if json.Unmarshal(data, &command) != nil || command.Method != "Runtime.evaluate" || command.SessionID != "isolated-tab" {
			t.Error("wrong browser command")
		}
		d.replies <- []byte(`{"method":"Page.loadEventFired"}`)
		reply, _ := json.Marshal(map[string]any{"id": command.ID, "result": map[string]any{"result": map[string]string{"value": "result cards only"}}})
		d.replies <- reply
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	value, err := d.evaluate(ctx, "isolated-tab", "document.title")
	if err != nil || value != "result cards only" {
		t.Fatal(value, err)
	}
}
func TestBrowserExceptionNeverPrintsPageDetails(t *testing.T) {
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	d := &devtools{input: writer, replies: make(chan []byte, 1), done: make(chan struct{})}
	go func() {
		data := make([]byte, 4096)
		_, _ = reader.Read(data)
		d.replies <- []byte(`{"id":1,"result":{"exceptionDetails":{"text":"sensitive page data"}}}`)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := d.evaluate(ctx, "tab", "secret expression")
	if err == nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatal("browser exception disclosed page data")
	}
}
