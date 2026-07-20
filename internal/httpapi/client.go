package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"memory-mcp/internal/db"
)

// Client 實作 db.Store，把每個操作轉發到遠端 memory-mcp REST API。
// 給 --remote 用，透過 SSH tunnel 之類的私有連線存取中央機器的資料庫。
type Client struct {
	baseURL string
	hc      *http.Client
}

// NewClient 建立指向 baseURL（例如 http://127.0.0.1:8766）的遠端 Store。
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		hc:      &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) do(method, path string, query url.Values, body, out any) error {
	u := c.baseURL + path
	if query != nil {
		u += "?" + query.Encode()
	}

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, u, reqBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("remote request to %s: %w", u, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var errBody struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&errBody)
		if errBody.Error != "" {
			return fmt.Errorf("remote: %s", errBody.Error)
		}
		return fmt.Errorf("remote: unexpected status %d", resp.StatusCode)
	}

	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Store 見 db.Store。
func (c *Client) Store(mem *db.Memory) (int64, error) {
	var out struct {
		ID int64 `json:"id"`
	}
	if err := c.do(http.MethodPost, "/v1/store", nil, mem, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

// Get 見 db.Store。
func (c *Client) Get(id int64) (*db.Memory, error) {
	var m db.Memory
	if err := c.do(http.MethodGet, "/v1/memories/"+strconv.FormatInt(id, 10), nil, nil, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Update 見 db.Store。
func (c *Client) Update(id int64, content string) error {
	body := map[string]string{"content": content}
	return c.do(http.MethodPut, "/v1/memories/"+strconv.FormatInt(id, 10), nil, body, nil)
}

// Delete 見 db.Store。
func (c *Client) Delete(id int64) error {
	return c.do(http.MethodDelete, "/v1/memories/"+strconv.FormatInt(id, 10), nil, nil, nil)
}

// List 見 db.Store。
func (c *Client) List(opts db.ListOptions) ([]db.Memory, error) {
	q := url.Values{}
	setIfNonEmpty(q, "type", opts.Type)
	setIfNonEmpty(q, "since", opts.Since)
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	var memories []db.Memory
	if err := c.do(http.MethodGet, "/v1/list", q, nil, &memories); err != nil {
		return nil, err
	}
	return memories, nil
}

// Search 見 db.Store。
func (c *Client) Search(opts db.SearchOptions) ([]db.SearchResult, error) {
	q := url.Values{}
	q.Set("q", opts.Query)
	setIfNonEmpty(q, "type", opts.Type)
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	var results []db.SearchResult
	if err := c.do(http.MethodGet, "/v1/search", q, nil, &results); err != nil {
		return nil, err
	}
	return results, nil
}

// Stats 見 db.Store。
func (c *Client) Stats() (*db.Stats, error) {
	var s db.Stats
	if err := c.do(http.MethodGet, "/v1/stats", nil, nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Context 見 db.Store。
func (c *Client) Context(opts db.ContextOptions) (string, error) {
	q := url.Values{}
	setIfNonEmpty(q, "type", opts.Type)
	setIfNonEmpty(q, "project", opts.Project)
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}

	u := c.baseURL + "/v1/context?" + q.Encode()
	resp, err := c.hc.Get(u)
	if err != nil {
		return "", fmt.Errorf("remote request to %s: %w", u, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("remote: unexpected status %d: %s", resp.StatusCode, data)
	}
	return string(data), nil
}

// ExportAll 見 db.Store。
func (c *Client) ExportAll() ([]db.Memory, error) {
	var memories []db.Memory
	if err := c.do(http.MethodGet, "/v1/export", nil, nil, &memories); err != nil {
		return nil, err
	}
	return memories, nil
}

// ImportBatch 見 db.Store。
func (c *Client) ImportBatch(memories []db.Memory) (int, error) {
	var out struct {
		Imported int `json:"imported"`
	}
	if err := c.do(http.MethodPost, "/v1/import", nil, memories, &out); err != nil {
		return 0, err
	}
	return out.Imported, nil
}

// Close 遠端 Client 沒有本機連線需要關閉。
func (c *Client) Close() error {
	return nil
}

func setIfNonEmpty(q url.Values, key, val string) {
	if val != "" {
		q.Set(key, val)
	}
}
