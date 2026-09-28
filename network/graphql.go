// Package network provides a client for interacting with a Dusk Rusk node.
//
// HTTP reads and streams honor context cancellation and a configurable default
// deadline. RUES subscriptions use a real WebSocket session.
package network

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/HDauven/w3sper.go/datadriver"
)

// DefaultVersion is the rusk-version header value sent with GraphQL requests.
const DefaultVersion = "1.0.0-rc.0"

// Canonical network URLs.
const (
	LOCALNET = "127.0.0.1:8080"
	MAINNET  = "mainnet.dusk.network"
	TESTNET  = "testnet.dusk.network"
	DEVNET   = "devnet.dusk.network"
)

// GraphQLError is returned when a GraphQL response contains errors.
type GraphQLError struct {
	Message   string         `json:"message"`
	Locations []interface{}  `json:"locations"`
	Data      interface{}    `json:"data"`
	Errors    []GraphQLError `json:"errors"`
}

func (e *GraphQLError) Error() string {
	if e.Message == "" && len(e.Errors) > 0 {
		return e.Errors[0].Message
	}
	return e.Message
}

// Request is a GraphQL request body.
type Request struct {
	Query         string                 `json:"query"`
	Variables     map[string]interface{} `json:"variables,omitempty"`
	OperationName string                 `json:"operationName,omitempty"`
}

// Client is a GraphQL-over-HTTP client for a Rusk node.
type Client struct {
	baseURL     *url.URL
	version     string
	http        *http.Client
	timeout     time.Duration
	mu          sync.Mutex
	session     *ruesSession
	connecting  *connectAttempt
	DataDrivers *datadriver.Registry
}

// New returns a GraphQL client for the given node URL.
func New(nodeURL string, opts ...Option) (*Client, error) {
	u, err := url.Parse(nodeURL)
	if err != nil {
		return nil, fmt.Errorf("network: invalid URL %q: %w", nodeURL, err)
	}
	if u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("network: %q is not an http(s) URL", nodeURL)
	}

	c := &Client{
		baseURL:     u,
		version:     DefaultVersion,
		http:        http.DefaultClient,
		timeout:     120 * time.Second,
		DataDrivers: datadriver.NewRegistry(),
	}
	for _, o := range opts {
		o(c)
	}
	if c.timeout < 0 || c.http == nil {
		return nil, fmt.Errorf("network: invalid timeout or HTTP client")
	}
	return c, nil
}

// Option configures a Client.
type Option func(*Client)

// WithVersion overrides the rusk-version header value.
func WithVersion(v string) Option {
	return func(c *Client) { c.version = v }
}

// WithHTTPClient overrides the underlying HTTP client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.http = hc }
}

// URL returns the node URL (without path).
func (c *Client) URL() *url.URL {
	cp := *c.baseURL
	cp.Path = ""
	cp.RawPath = ""
	cp.RawQuery = ""
	return &cp
}

// Query executes a GraphQL query and decodes the `data` object into out.
func (c *Client) Query(ctx context.Context, gql string, opts ...QueryOption) (map[string]json.RawMessage, error) {
	ctx, cancel := c.ReadContext(ctx)
	defer cancel()
	req, err := c.newQueryRequest(gql, opts...)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return parseResponse(resp)
}

// QueryRaw executes a GraphQL query, returning the raw response bytes.
func (c *Client) QueryRaw(ctx context.Context, gql string, opts ...QueryOption) ([]byte, error) {
	ctx, cancel := c.ReadContext(ctx)
	defer cancel()
	req, err := c.newQueryRequest(gql, opts...)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := checkStatus(resp, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (c *Client) newQueryRequest(gql string, opts ...QueryOption) (*http.Request, error) {
	query := strings.TrimSpace(gql)
	if query == "" {
		return nil, fmt.Errorf("network: graphql query is required")
	}
	if !isGraphQLDocument(query) {
		query = "query { " + query + " }"
	}

	payload := Request{Query: query}
	for _, o := range opts {
		o(&payload)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	u, _ := url.Parse(c.baseURL.String())
	u.Path = "/graphql"
	u.RawPath, u.RawQuery = "", ""

	req, err := http.NewRequest(http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/graphql-response+json, application/json")
	req.Header.Set("rusk-version", c.version)

	return req, nil
}

// QueryOption mutates a GraphQL request before it is sent.
type QueryOption func(*Request)

// WithVariables sets the GraphQL variables for the request.
func WithVariables(vars map[string]interface{}) QueryOption {
	return func(r *Request) { r.Variables = vars }
}

func parseResponse(resp *http.Response) (map[string]json.RawMessage, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var payload struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []GraphQLError             `json:"errors"`
	}

	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				return nil, &HTTPError{resp.StatusCode, string(body), "/graphql"}
			}
			return nil, fmt.Errorf("network: invalid GraphQL response: %w", err)
		}
	}

	if len(payload.Errors) > 0 {
		return payload.Data, &GraphQLError{Errors: payload.Errors, Data: payload.Data}
	}

	if err := checkStatus(resp, body); err != nil {
		return nil, err
	}

	if payload.Data == nil {
		return nil, fmt.Errorf("network: invalid GraphQL response: missing data/errors")
	}
	return payload.Data, nil
}

func checkStatus(resp *http.Response, body []byte) error {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{resp.StatusCode, string(body), "/graphql"}
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return fmt.Errorf("network: invalid GraphQL response: missing data/errors")
	}
	return nil
}

func isGraphQLDocument(query string) bool {
	return startsWithAny(query, "query", "mutation", "subscription", "fragment") ||
		strings.HasPrefix(query, "{")
}

func startsWithAny(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) && (len(s) == len(p) || isSpaceOrBraces(s[len(p)])) {
			return true
		}
	}
	return false
}

func isSpaceOrBraces(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '{' || c == '('
}
