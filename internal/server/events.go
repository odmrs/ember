package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/odmrs/ember/internal/connector"
)

// events streams the Connector's state as Server-Sent Events. An open stream
// is a Session: the Connector runs while at least one is connected, and the
// Session ends when the tab closes the stream.
func events(conn *connector.Connector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		states, end := conn.Session()
		defer end()

		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		// EventSource reconnects on its own; ask it to come back within the
		// Connector's own retry period.
		if _, err := fmt.Fprint(w, "retry: 2000\n\n"); err != nil {
			return
		}
		for {
			if err := rc.Flush(); err != nil {
				return
			}
			select {
			case <-r.Context().Done():
				return
			case s, open := <-states:
				if !open {
					return
				}
				data, err := json.Marshal(s)
				if err != nil {
					return
				}
				if _, err := fmt.Fprintf(w, "event: state\ndata: %s\n\n", data); err != nil {
					return
				}
			}
		}
	}
}
