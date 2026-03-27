package aperiodic

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

const (
	DefaultBaseURL = "https://aperiodic.io/api/v1"
	DefaultTimeout = 60 * time.Second
)

type APIError struct {
	StatusCode int
	Message    string
	Details    []string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%d: %s", e.StatusCode, e.Message)
}

type AperiodicClient struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

func NewAperiodicClient(apiKey string) *AperiodicClient {
	baseURL := os.Getenv("APERIODIC_API_URL")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}

	return &AperiodicClient{
		APIKey:  apiKey,
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: DefaultTimeout,
		},
	}
}

func (c *AperiodicClient) getHeaders() http.Header {
	header := make(http.Header)
	header.Set("X-API-KEY", c.APIKey)

	cfClientID := os.Getenv("CF_ACCESS_CLIENT_ID")
	cfClientSecret := os.Getenv("CF_ACCESS_CLIENT_SECRET")
	if cfClientID != "" && cfClientSecret != "" {
		header.Set("CF-Access-Client-Id", cfClientID)
		header.Set("CF-Access-Client-Secret", cfClientSecret)
	}

	return header
}

func (c *AperiodicClient) handleAPIError(resp *http.Response) error {
	if resp.StatusCode == http.StatusOK {
		return nil
	}

	apiErr := &APIError{
		StatusCode: resp.StatusCode,
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		apiErr.Message = "Unauthorized"
	case http.StatusForbidden:
		apiErr.Message = "Forbidden"
	case http.StatusNotFound:
		apiErr.Message = "Not Found"
	case http.StatusTooManyRequests:
		apiErr.Message = "Too Many Requests"
	default:
		var errResp APIErrorResponse
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err == nil {
			apiErr.Message = errResp.Error
			apiErr.Details = errResp.Details
		} else {
			body, _ := io.ReadAll(resp.Body)
			apiErr.Message = string(body)
		}
	}

	if apiErr.Message == "" {
		apiErr.Message = "Unknown error"
	}

	return apiErr
}

func (c *AperiodicClient) GetSymbols(exchange string) ([]string, error) {
	u, err := url.Parse(fmt.Sprintf("%s/symbols/%s", c.BaseURL, exchange))
	if err != nil {
		return nil, err
	}

	req := &http.Request{
		Method: http.MethodGet,
		URL:    u,
		Header: c.getHeaders(),
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := c.handleAPIError(resp); err != nil {
		return nil, err
	}

	var symResp SymbolsResponse
	if err := json.NewDecoder(resp.Body).Decode(&symResp); err != nil {
		return nil, err
	}

	return symResp.Symbols, nil
}

func (c *AperiodicClient) FetchPresignedUrls(bucket string, timestamp TimestampType, interval Interval, exchange string, symbol string, startDate string, endDate string) (*AggregateDataResponse, error) {
	u, err := url.Parse(fmt.Sprintf("%s/data/%s", c.BaseURL, bucket))
	if err != nil {
		return nil, err
	}

	q := u.Query()
	q.Set("timestamp", string(timestamp))
	q.Set("interval", string(interval))
	q.Set("exchange", exchange)
	q.Set("symbol", symbol)
	q.Set("start_date", startDate)
	q.Set("end_date", endDate)
	u.RawQuery = q.Encode()

	req := &http.Request{
		Method: http.MethodGet,
		URL:    u,
		Header: c.getHeaders(),
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if err := c.handleAPIError(resp); err != nil {
		return nil, err
	}

	var dataResp AggregateDataResponse
	if err := json.NewDecoder(resp.Body).Decode(&dataResp); err != nil {
		return nil, err
	}

	return &dataResp, nil
}
