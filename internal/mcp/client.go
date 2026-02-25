package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gal-cli/gal-cli/internal/config"
	"github.com/gal-cli/gal-cli/internal/provider"
)

type Client struct {
	url     string
	headers map[string]string
	id      int
	http    *http.Client
}

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func NewClient(conf config.MCPConf) *Client {
	timeout := conf.Timeout
	if timeout <= 0 {
		timeout = 30
	}
	return &Client{
		url:     conf.URL,
		headers: conf.Headers,
		http:    &http.Client{Timeout: time.Duration(timeout) * time.Second},
	}
}

func (c *Client) Initialize() error {
	_, err := c.call("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "gal-cli", "version": "1.0"},
	})
	return err
}

func (c *Client) ListTools() ([]provider.ToolDef, error) {
	raw, err := c.call("tools/list", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parse tools/list: %w", err)
	}
	var defs []provider.ToolDef
	for _, t := range result.Tools {
		defs = append(defs, provider.ToolDef{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.InputSchema,
		})
	}
	return defs, nil
}

func (c *Client) CallTool(name string, args map[string]any) (string, error) {
	raw, err := c.call("tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return "", err
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return string(raw), nil
	}
	var sb strings.Builder
	for _, block := range result.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	return sb.String(), nil
}

func (c *Client) call(method string, params any) (json.RawMessage, error) {
	c.id++
	req := jsonRPCRequest{JSONRPC: "2.0", ID: c.id, Method: method, Params: params}
	body, _ := json.Marshal(req)

	httpReq, err := http.NewRequest("POST", c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("mcp request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("mcp HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var rpcResp jsonRPCResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("mcp parse response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("mcp error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}
