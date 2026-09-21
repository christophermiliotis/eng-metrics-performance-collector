// Package github is a small, dependency-free GitHub REST v3 client covering
// exactly the endpoints this tool needs: pull requests, reviews, issue
// comments, workflow runs, the commit-compare API, and repository metadata.
//
// It is deliberately minimal rather than a general-purpose SDK. It handles
// pagination via the Link header and transparently waits out primary
// rate-limit exhaustion so long-running snapshots don't fail mid-collection.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to a single GitHub REST API host.
type Client struct {
	httpClient *http.Client
	baseURL    string
	token      string
	userAgent  string
}

// NewClient builds a client for the given API base URL (no trailing slash) and
// PAT. timeout bounds each individual request.
func NewClient(baseURL, token string, timeout time.Duration) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    baseURL,
		token:      token,
		userAgent:  "engineering-performance-metrics/1.0",
	}
}

// get performs a single authenticated GET against path (which may be absolute,
// as returned in Link headers, or relative to baseURL) and decodes the JSON
// body into out. It retries once after waiting when the primary rate limit is
// hit.
func (c *Client) get(ctx context.Context, rawURL string, out any) (*http.Response, error) {
	full := rawURL
	if strings.HasPrefix(rawURL, "/") {
		full = c.baseURL + rawURL
	}

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", c.userAgent)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("GET %s: %w", full, err)
		}

		// Primary rate limit: 403/429 with remaining == 0. Wait until reset
		// (once) and retry, so a big backfill doesn't abort.
		if (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) &&
			resp.Header.Get("X-RateLimit-Remaining") == "0" && attempt == 0 {
			wait := rateLimitWait(resp.Header)
			resp.Body.Close()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
				continue
			}
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			resp.Body.Close()
			return nil, fmt.Errorf("GET %s: unexpected status %s: %s", full, resp.Status, strings.TrimSpace(string(body)))
		}

		if out != nil {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				resp.Body.Close()
				return nil, fmt.Errorf("decoding response from %s: %w", full, err)
			}
		}
		return resp, nil
	}
}

// rateLimitWait computes how long to sleep before retrying, based on the
// X-RateLimit-Reset epoch header. Falls back to 60s if absent. We cannot call
// time.Now() in some sandboxes, but here we genuinely need wall-clock to
// compute the delta; the standard library handles it.
func rateLimitWait(h http.Header) time.Duration {
	resetEpoch, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64)
	if err != nil {
		return 60 * time.Second
	}
	d := time.Until(time.Unix(resetEpoch, 0)) + 2*time.Second
	if d < 0 {
		return time.Second
	}
	return d
}

// getPaginated walks every page of a list endpoint, invoking decode for each
// page's raw body and following the rel="next" Link header until exhausted.
func (c *Client) getPaginated(ctx context.Context, rawURL string, page func(body []byte) (stop bool, err error)) error {
	next := rawURL
	for next != "" {
		req, err := c.buildReq(ctx, next)
		if err != nil {
			return err
		}
		resp, err := c.doWithRetry(ctx, req)
		if err != nil {
			return err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("reading page body: %w", err)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("GET %s: unexpected status %s: %s", req.URL, resp.Status, strings.TrimSpace(string(body[:min(len(body), 2048)])))
		}
		stop, err := page(body)
		if err != nil {
			return err
		}
		if stop {
			return nil
		}
		next = nextLink(resp.Header.Get("Link"))
	}
	return nil
}

func (c *Client) buildReq(ctx context.Context, rawURL string) (*http.Request, error) {
	full := rawURL
	if strings.HasPrefix(rawURL, "/") {
		full = c.baseURL + rawURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", c.userAgent)
	return req, nil
}

func (c *Client) doWithRetry(ctx context.Context, req *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		// Clone so a retried request is fresh.
		r := req.Clone(ctx)
		resp, err := c.httpClient.Do(r)
		if err != nil {
			return nil, fmt.Errorf("GET %s: %w", req.URL, err)
		}
		if (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) &&
			resp.Header.Get("X-RateLimit-Remaining") == "0" && attempt == 0 {
			wait := rateLimitWait(resp.Header)
			resp.Body.Close()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
				continue
			}
		}
		return resp, nil
	}
}

// nextLink extracts the rel="next" URL from a GitHub Link header, or "" if none.
func nextLink(link string) string {
	if link == "" {
		return ""
	}
	for _, part := range strings.Split(link, ",") {
		segs := strings.Split(strings.TrimSpace(part), ";")
		if len(segs) < 2 {
			continue
		}
		urlPart := strings.TrimSpace(segs[0])
		if !strings.HasPrefix(urlPart, "<") || !strings.HasSuffix(urlPart, ">") {
			continue
		}
		for _, s := range segs[1:] {
			if strings.TrimSpace(s) == `rel="next"` {
				return urlPart[1 : len(urlPart)-1]
			}
		}
	}
	return ""
}

// q builds a query string from key/value pairs.
func q(pairs ...string) string {
	v := url.Values{}
	for i := 0; i+1 < len(pairs); i += 2 {
		v.Set(pairs[i], pairs[i+1])
	}
	return v.Encode()
}
