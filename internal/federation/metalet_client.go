package federation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultMetaletTimeout = 10 * time.Second
	metaletWalletAPIPath  = "/wallet-api"
)

// MetaletClient calls the Metalet MVC wallet API endpoints used by federation.
type MetaletClient struct {
	baseURL    *url.URL
	httpClient *http.Client
}

// MetaletClientOption customizes Metalet client behavior.
type MetaletClientOption func(*metaletClientOptions)

type metaletClientOptions struct {
	httpClient *http.Client
	timeout    time.Duration
	timeoutSet bool
}

// WithMetaletHTTPClient sets the HTTP client used for API requests.
func WithMetaletHTTPClient(client *http.Client) MetaletClientOption {
	return func(opts *metaletClientOptions) {
		if client != nil {
			opts.httpClient = client
		}
	}
}

// WithMetaletTimeout sets the HTTP client timeout used for API requests.
func WithMetaletTimeout(timeout time.Duration) MetaletClientOption {
	return func(opts *metaletClientOptions) {
		if timeout > 0 {
			opts.timeout = timeout
			opts.timeoutSet = true
		}
	}
}

// MVCUTXO is one UTXO item returned by Metalet's MVC address UTXO endpoint.
type MVCUTXO struct {
	TxID     string `json:"txid"`
	OutIndex int    `json:"outIndex"`
	Value    int64  `json:"value"`
	Address  string `json:"address"`
	Height   int    `json:"height"`
	Flag     string `json:"flag"`
}

// MVCBroadcastRequest is the request body for Metalet's MVC broadcast endpoint.
type MVCBroadcastRequest struct {
	Chain     string `json:"chain"`
	Net       string `json:"net"`
	PublicKey string `json:"publicKey"`
	RawTx     string `json:"rawTx"`
}

// MetaletError is returned when Metalet responds with a non-2xx status or nonzero API code.
type MetaletError struct {
	StatusCode int
	Code       int
	Message    string
	Body       string
}

func (e *MetaletError) Error() string {
	if e == nil {
		return "metalet API error"
	}
	if e.Code != 0 || e.Message != "" {
		return fmt.Sprintf("metalet API returned status %d code %d: %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("metalet API returned status %d: %s", e.StatusCode, e.Body)
}

// NewMetaletClient returns a client for Metalet MVC wallet API endpoints.
func NewMetaletClient(baseURL string, opts ...MetaletClientOption) (*MetaletClient, error) {
	normalizedBaseURL, err := normalizeMetaletBaseURL(baseURL)
	if err != nil {
		return nil, err
	}

	options := metaletClientOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	httpClient := options.httpClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultMetaletTimeout}
	} else {
		clientCopy := *httpClient
		httpClient = &clientCopy
	}
	if options.timeoutSet {
		httpClient.Timeout = options.timeout
	}

	return &MetaletClient{
		baseURL:    normalizedBaseURL,
		httpClient: httpClient,
	}, nil
}

// MVCAddressUTXOs fetches MVC UTXOs for an address.
func (c *MetaletClient) MVCAddressUTXOs(ctx context.Context, net string, address string, flag string) ([]MVCUTXO, error) {
	endpoint := c.endpoint("/v4/mvc/address/utxo-list")
	query := endpoint.Query()
	query.Set("net", net)
	query.Set("address", address)
	if flag != "" {
		query.Set("flag", flag)
	}
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(metaletContext(ctx), http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create metalet utxo request: %w", err)
	}

	body, statusCode, err := c.do(req)
	if err != nil {
		return nil, err
	}
	body, err = metaletResponseData(body, statusCode)
	if err != nil {
		return nil, err
	}

	var response struct {
		List []MVCUTXO `json:"list"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decode metalet utxo response: %w", err)
	}
	if response.List == nil {
		return []MVCUTXO{}, nil
	}
	return response.List, nil
}

// BroadcastMVC broadcasts a raw MVC transaction through Metalet.
func (c *MetaletClient) BroadcastMVC(ctx context.Context, broadcast MVCBroadcastRequest) (string, error) {
	payload, err := json.Marshal(broadcast)
	if err != nil {
		return "", fmt.Errorf("encode metalet broadcast request: %w", err)
	}

	req, err := http.NewRequestWithContext(metaletContext(ctx), http.MethodPost, c.endpoint("/v4/mvc/tx/broadcast").String(), bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create metalet broadcast request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	body, statusCode, err := c.do(req)
	if err != nil {
		return "", err
	}
	body, err = metaletResponseData(body, statusCode)
	if err != nil {
		return "", err
	}

	var result string
	if err := json.Unmarshal(body, &result); err == nil {
		return result, nil
	}
	return strings.TrimSpace(string(body)), nil
}

func (c *MetaletClient) do(req *http.Request) ([]byte, int, error) {
	if c == nil || c.httpClient == nil {
		return nil, 0, fmt.Errorf("metalet client is not initialized")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read metalet response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, resp.StatusCode, &MetaletError{
			StatusCode: resp.StatusCode,
			Body:       string(body),
		}
	}
	return body, resp.StatusCode, nil
}

type metaletEnvelope struct {
	Code           int             `json:"code"`
	Message        string          `json:"message"`
	ProcessingTime int             `json:"processingTime"`
	Data           json.RawMessage `json:"data"`
}

func metaletResponseData(body []byte, statusCode int) ([]byte, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(body, &probe); err != nil {
		return body, nil
	}
	if _, ok := probe["code"]; !ok {
		if _, ok := probe["data"]; !ok {
			return body, nil
		}
	}

	var envelope metaletEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode metalet response envelope: %w", err)
	}
	if envelope.Code != 0 {
		return nil, &MetaletError{
			StatusCode: statusCode,
			Code:       envelope.Code,
			Message:    envelope.Message,
			Body:       string(body),
		}
	}
	if envelope.Data == nil {
		return []byte("null"), nil
	}
	return envelope.Data, nil
}

func (c *MetaletClient) endpoint(path string) *url.URL {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	return &endpoint
}

func normalizeMetaletBaseURL(baseURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("parse metalet base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("metalet base URL requires scheme and host")
	}

	path := strings.TrimRight(parsed.Path, "/")
	if path == "" {
		path = metaletWalletAPIPath
	} else if !strings.HasSuffix(path, metaletWalletAPIPath) {
		path += metaletWalletAPIPath
	}
	parsed.Path = path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func metaletContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
