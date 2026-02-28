package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gal-cli/gal-cli/internal/provider"
)

const (
	maxResponseSize = 10 << 20 // 10MB
	maxBodyPreview  = 4096     // body truncated to 4KB for LLM context
	maxTimeout      = 300
	defaultTimeout  = 30
)

func (r *Registry) registerHTTP() {
	r.RegisterReadOnly(provider.ToolDef{
		Name:        "http",
		Description: "Make HTTP requests to any URL. This is the preferred tool for all HTTP/API requests — use this instead of curl/wget in bash. Supports all RESTful methods (GET, POST, PUT, DELETE, PATCH, HEAD, OPTIONS). Returns structured JSON with status, headers, body, size, and timing. Use for API calls, web scraping, health checks, and webhooks. For sensitive data (API keys, tokens), use the 'interactive' tool to collect them first, then pass via headers.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"method":           map[string]any{"type": "string", "description": "HTTP method: GET, POST, PUT, DELETE, PATCH, HEAD, OPTIONS"},
				"url":              map[string]any{"type": "string", "description": "Complete URL including protocol"},
				"headers":          map[string]any{"type": "object", "description": "Request headers (key-value pairs)"},
				"body":             map[string]any{"type": "string", "description": "Request body (for POST/PUT/PATCH)"},
				"query":            map[string]any{"type": "object", "description": "Query parameters (automatically URL-encoded)"},
				"timeout":          map[string]any{"type": "integer", "description": "Timeout in seconds (default 30, max 300)"},
				"follow_redirects": map[string]any{"type": "boolean", "description": "Whether to follow HTTP redirects (default true)"},
			},
			"required": []string{"method", "url"},
		},
	}, func(ctx context.Context, args map[string]any) (string, error) {
		method := strings.ToUpper(getStr(args, "method"))
		rawURL := getStr(args, "url")
		if rawURL == "" {
			return errJSON("url is required"), nil
		}
		if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
			rawURL = "http://" + rawURL
		}
		body := getStr(args, "body")
		timeout := toInt(args["timeout"])
		if timeout <= 0 {
			timeout = defaultTimeout
		}
		if timeout > maxTimeout {
			timeout = maxTimeout
		}

		// build URL with query params
		parsedURL, err := url.Parse(rawURL)
		if err != nil {
			return errJSON("invalid URL: " + err.Error()), nil
		}
		if query, ok := args["query"].(map[string]any); ok {
			q := parsedURL.Query()
			for k, v := range query {
				q.Set(k, fmt.Sprint(v))
			}
			parsedURL.RawQuery = q.Encode()
		}

		// build request
		var bodyReader io.Reader
		if body != "" {
			bodyReader = strings.NewReader(body)
		}
		ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, method, parsedURL.String(), bodyReader)
		if err != nil {
			return errJSON(err.Error()), nil
		}
		req.Header.Set("User-Agent", "GAL-CLI/1.0")
		if headers, ok := args["headers"].(map[string]any); ok {
			for k, v := range headers {
				req.Header.Set(k, fmt.Sprint(v))
			}
		}

		// configure client
		client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
		if follow, ok := args["follow_redirects"].(bool); ok && !follow {
			client.CheckRedirect = func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}
		}

		// execute
		start := time.Now()
		resp, err := client.Do(req)
		elapsed := time.Since(start).Milliseconds()
		if err != nil {
			return errJSON(err.Error()), nil
		}
		defer resp.Body.Close()

		// read body (capped)
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))

		// collect response headers
		respHeaders := make(map[string]string)
		for k := range resp.Header {
			respHeaders[k] = resp.Header.Get(k)
		}

		// truncate body for LLM context (keep full size info)
		bodyStr := string(respBody)
		truncated := false
		if len(bodyStr) > maxBodyPreview {
			bodyStr = bodyStr[:maxBodyPreview] + "...(truncated)"
			truncated = true
		}

		result, _ := json.Marshal(map[string]any{
			"status":      resp.StatusCode,
			"status_text": resp.Status,
			"headers":     respHeaders,
			"body":        bodyStr,
			"size":        len(respBody),
			"truncated":   truncated,
			"time_ms":     elapsed,
		})
		return string(result), nil
	})
}

func getStr(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func errJSON(msg string) string {
	b, _ := json.Marshal(map[string]any{"error": msg, "status": 0})
	return string(b)
}
