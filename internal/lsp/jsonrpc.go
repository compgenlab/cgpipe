package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// readMessage reads one LSP base-protocol message: a set of `Header: value`
// lines (we only care about Content-Length) terminated by a blank line, then
// exactly Content-Length bytes of JSON payload. It returns io.EOF when the
// stream is exhausted at a message boundary.
func readMessage(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // end of headers
		}
		if v, ok := strings.CutPrefix(line, "Content-Length:"); ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return nil, fmt.Errorf("invalid Content-Length: %q", v)
			}
			length = n
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("message missing Content-Length header")
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// writeMessage marshals v and writes it as a framed LSP message. Writes are
// serialized by mu so notifications and responses never interleave on the wire.
func writeMessage(w io.Writer, mu *sync.Mutex, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
