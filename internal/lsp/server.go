// Package lsp implements a minimal Language Server Protocol server for the cgpipe
// language, exposed as `cgpipe lsp`. It speaks JSON-RPC 2.0 over stdio and reuses
// cgpipe's own lexer and parser to provide semantic tokens, parse diagnostics,
// hover, and completion.
//
// The protocol layer is hand-written against encoding/json rather than built on
// an LSP framework: the surface we need is small and stable, and the available
// Go LSP libraries pull in logging frameworks and several transitive modules,
// which would be out of proportion for a project that otherwise has a single
// direct dependency.
package lsp

import (
	"bufio"
	"encoding/json"
	"io"
	"sync"

	"github.com/compgenlab/cgpipe/internal/buildinfo"
)

// Server holds the open-document state and serializes writes to the client.
type Server struct {
	in  *bufio.Reader
	out io.Writer

	writeMu sync.Mutex // serializes framed writes to out

	docMu sync.Mutex
	docs  map[string]string // uri -> full text (textDocumentSync = Full)
}

// Run serves the LSP protocol over in/out until the client sends `exit` or the
// input stream closes. All protocol traffic is on out; callers must keep any
// logging on a separate stream (stderr).
func Run(in io.Reader, out io.Writer) error {
	s := &Server{
		in:   bufio.NewReader(in),
		out:  out,
		docs: make(map[string]string),
	}
	return s.loop()
}

func (s *Server) loop() error {
	for {
		data, err := readMessage(s.in)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		var req request
		if err := json.Unmarshal(data, &req); err != nil {
			continue // ignore malformed frames
		}
		if stop := s.dispatch(req); stop {
			return nil
		}
	}
}

// dispatch handles one message and returns true when the server should stop.
func (s *Server) dispatch(req request) bool {
	switch req.Method {
	case "initialize":
		s.reply(req.ID, s.initializeResult())
	case "initialized", "$/setTrace", "$/cancelRequest", "workspace/didChangeConfiguration":
		// notifications we accept but don't act on
	case "shutdown":
		s.reply(req.ID, nil)
	case "exit":
		return true

	case "textDocument/didOpen":
		var p didOpenParams
		if json.Unmarshal(req.Params, &p) == nil {
			s.setDoc(p.TextDocument.URI, p.TextDocument.Text)
			s.publishDiagnostics(p.TextDocument.URI)
		}
	case "textDocument/didChange":
		var p didChangeParams
		if json.Unmarshal(req.Params, &p) == nil && len(p.ContentChanges) > 0 {
			s.setDoc(p.TextDocument.URI, p.ContentChanges[len(p.ContentChanges)-1].Text)
			s.publishDiagnostics(p.TextDocument.URI)
		}
	case "textDocument/didClose":
		var p docIDParams
		if json.Unmarshal(req.Params, &p) == nil {
			s.delDoc(p.TextDocument.URI)
		}

	case "textDocument/semanticTokens/full":
		var p docIDParams
		json.Unmarshal(req.Params, &p)
		text := s.getDoc(p.TextDocument.URI)
		s.reply(req.ID, semanticTokensResult{Data: semanticTokens(text, p.TextDocument.URI)})

	case "textDocument/hover":
		var p textDocumentPositionParams
		json.Unmarshal(req.Params, &p)
		s.reply(req.ID, hoverAt(s.getDoc(p.TextDocument.URI), p.TextDocument.URI, p.Position))

	case "textDocument/completion":
		var p textDocumentPositionParams
		json.Unmarshal(req.Params, &p)
		s.reply(req.ID, completions(s.getDoc(p.TextDocument.URI), p.TextDocument.URI))

	default:
		// Unknown request: reply with null so the client doesn't block.
		// Unknown notifications (no id) are ignored.
		if len(req.ID) > 0 {
			s.reply(req.ID, nil)
		}
	}
	return false
}

func (s *Server) initializeResult() initializeResult {
	return initializeResult{
		Capabilities: serverCapabilities{
			TextDocumentSync: 1, // Full
			SemanticTokensProvider: semanticTokensOptions{
				Legend: semanticTokensLegend{
					TokenTypes:     semanticTokenTypes,
					TokenModifiers: []string{},
				},
				Full: true,
			},
			HoverProvider:      true,
			CompletionProvider: completionOptions{},
		},
		ServerInfo: serverInfo{Name: "cgpipe", Version: buildinfo.Version},
	}
}

func (s *Server) reply(id json.RawMessage, result any) {
	if len(id) == 0 {
		return // never reply to a notification
	}
	writeMessage(s.out, &s.writeMu, response{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *Server) notify(method string, params any) {
	writeMessage(s.out, &s.writeMu, notification{JSONRPC: "2.0", Method: method, Params: params})
}

func (s *Server) publishDiagnostics(uri string) {
	text := s.getDoc(uri)
	s.notify("textDocument/publishDiagnostics", publishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diagnostics(text, uri),
	})
}

func (s *Server) setDoc(uri, text string) {
	s.docMu.Lock()
	s.docs[uri] = text
	s.docMu.Unlock()
}

func (s *Server) getDoc(uri string) string {
	s.docMu.Lock()
	defer s.docMu.Unlock()
	return s.docs[uri]
}

func (s *Server) delDoc(uri string) {
	s.docMu.Lock()
	delete(s.docs, uri)
	s.docMu.Unlock()
}
