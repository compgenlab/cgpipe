package lsp

import "encoding/json"

// This file defines the minimal subset of the Language Server Protocol types
// that the cgpipe server actually uses. It is intentionally hand-written (rather
// than pulling a protocol library) to keep cgpipe's dependency set to one direct
// module. See package doc for the rationale.

// request is an incoming JSON-RPC message. Requests carry an ID and expect a
// response; notifications omit the ID and must not be answered.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// response is an outgoing reply to a request. Result is always present (null
// for void replies like shutdown); we never emit JSON-RPC errors, replying with
// a null result for unknown requests instead so clients don't hang.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result"`
}

// notification is an outgoing server-initiated message (e.g. publishDiagnostics).
type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// --- initialize ---

type initializeResult struct {
	Capabilities serverCapabilities `json:"capabilities"`
	ServerInfo   serverInfo         `json:"serverInfo"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type serverCapabilities struct {
	TextDocumentSync       int                   `json:"textDocumentSync"` // 1 = Full
	SemanticTokensProvider semanticTokensOptions `json:"semanticTokensProvider"`
	HoverProvider          bool                  `json:"hoverProvider"`
	CompletionProvider     completionOptions     `json:"completionProvider"`
}

type semanticTokensOptions struct {
	Legend semanticTokensLegend `json:"legend"`
	Full   bool                 `json:"full"`
}

type semanticTokensLegend struct {
	TokenTypes     []string `json:"tokenTypes"`
	TokenModifiers []string `json:"tokenModifiers"`
}

type completionOptions struct {
	TriggerCharacters []string `json:"triggerCharacters,omitempty"`
}

// --- text document sync ---

type didOpenParams struct {
	TextDocument struct {
		URI  string `json:"uri"`
		Text string `json:"text"`
	} `json:"textDocument"`
}

type didChangeParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	ContentChanges []struct {
		Text string `json:"text"`
	} `json:"contentChanges"`
}

type docIDParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
}

// --- positions, ranges, diagnostics ---

// Position is 0-based; Character is a UTF-16 code-unit offset within the line.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity,omitempty"` // 1 = Error
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
}

type publishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// --- semantic tokens ---

type semanticTokensResult struct {
	Data []uint32 `json:"data"`
}

// --- hover ---

type textDocumentPositionParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position Position `json:"position"`
}

type hoverResult struct {
	Contents markupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

type markupContent struct {
	Kind  string `json:"kind"` // "markdown"
	Value string `json:"value"`
}

// --- completion ---

// completionItemKind values from the LSP spec (subset).
const (
	ciFunction = 3
	ciVariable = 6
	ciKeyword  = 14
)

type completionItem struct {
	Label         string `json:"label"`
	Kind          int    `json:"kind,omitempty"`
	Detail        string `json:"detail,omitempty"`
	Documentation string `json:"documentation,omitempty"`
}
