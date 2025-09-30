package command

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

var (
	_ caddy.Module                = (*Middleware)(nil)
	_ caddy.Provisioner           = (*Middleware)(nil)
	_ caddy.Validator             = (*Middleware)(nil)
	_ caddyhttp.MiddlewareHandler = (*Middleware)(nil)
)

var runCommand = (*Cmd).run

func init() {
	caddy.RegisterModule(Middleware{})
}

// Middleware implements an HTTP handler that runs shell command.
type Middleware struct {
	Cmd
}

type execRequestPayload struct {
	Args  []string `json:"args"`
	Stdin *string  `json:"stdin"`
}

func isJSONContentType(value string) bool {
	if value == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}
	return mediaType == "application/json"
}

// CaddyModule returns the Caddy module information.
func (Middleware) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.exec",
		New: func() caddy.Module { return new(Middleware) },
	}
}

// Provision implements caddy.Provisioner.
func (m *Middleware) Provision(ctx caddy.Context) error { return m.Cmd.provision(ctx, m) }

// Validate implements caddy.Validator
func (m Middleware) Validate() error { return m.Cmd.validate() }

// ServeHTTP implements caddyhttp.MiddlewareHandler.
func (m Middleware) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	repl := r.Context().Value(caddy.ReplacerCtxKey).(*caddy.Replacer)

	var body []byte
	if r.Body != nil {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			return caddyhttp.Error(http.StatusBadRequest, fmt.Errorf("reading request body: %w", err))
		}
		r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
	}

	var payload execRequestPayload
	var stdin io.Reader
	if len(body) > 0 && isJSONContentType(r.Header.Get("Content-Type")) {
		if err := json.Unmarshal(body, &payload); err != nil {
			return caddyhttp.Error(http.StatusBadRequest, fmt.Errorf("decoding request body: %w", err))
		}
		if payload.Stdin != nil {
			stdin = strings.NewReader(*payload.Stdin)
		}
	}

	// replace per-request placeholders
	argv := make([]string, 0, len(m.Args)+len(payload.Args))
	for _, argument := range m.Args {
		argv = append(argv, repl.ReplaceAll(argument, ""))
	}
	for _, argument := range payload.Args {
		argv = append(argv, repl.ReplaceAll(argument, ""))
	}

	err := runCommand(&m.Cmd, argv, stdin)

	if m.PassThru {
		if err != nil {
			m.log.Error(err.Error())
		}

		return next.ServeHTTP(w, r)
	}

	var resp struct {
		Status string `json:"status,omitempty"`
		Error  string `json:"error,omitempty"`
	}

	if err == nil {
		resp.Status = "success"
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		resp.Error = err.Error()
	}

	w.Header().Add("content-type", "application/json")
	return json.NewEncoder(w).Encode(resp)
}

// Cleanup implements caddy.Cleanup
// TODO: ensure all running processes are terminated.
func (m *Middleware) Cleanup() error {
	return nil
}
