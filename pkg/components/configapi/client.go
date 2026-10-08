package configapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/portainer/kubesolo/internal/upgrade"
	"github.com/portainer/kubesolo/types"
)

// Client talks to a running KubeSolo's configuration API over its unix socket.
//
// It exists so that kubesoloctl can go through the API when KubeSolo is running,
// rather than editing the file underneath it. Doing so gets validation against
// the real host, the restart-required report, and the concurrency protection —
// none of which a direct file edit can offer.
type Client struct {
	socketPath string
	http       *http.Client
}

// NewClient returns a client for the socket at socketPath. It does not connect;
// use Available for that.
func NewClient(socketPath string) *Client {
	return &Client{
		socketPath: socketPath,
		http: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
				},
			},
			Timeout: clientTimeout,
		},
	}
}

const clientTimeout = 10 * time.Second

// Available reports whether something is listening on the socket.
//
// The socket file outliving its process is the normal case after an unclean
// shutdown, so its presence is not evidence of a running KubeSolo — only a
// successful connection is.
func (c *Client) Available() bool {
	conn, err := net.DialTimeout("unix", c.socketPath, time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Get reads the configuration. Secrets are redacted unless showSecrets is set.
func (c *Client) Get(showSecrets bool) (*Response, error) {
	path := "/api/v1/config"
	if showSecrets {
		path += "?showSecrets=true"
	}
	return c.do(http.MethodGet, path, nil, "")
}

// Patch applies an RFC 7386 merge patch.
func (c *Client) Patch(patch []byte) (*Response, error) {
	return c.do(http.MethodPatch, "/api/v1/config", patch, "application/merge-patch+json")
}

// Put replaces the configuration wholesale.
func (c *Client) Put(cfg *types.Config) (*Response, error) {
	body, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return c.do(http.MethodPut, "/api/v1/config", body, "application/json")
}

// Validate checks a candidate configuration without saving it.
func (c *Client) Validate(cfg *types.Config) (*Response, error) {
	body, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	return c.do(http.MethodPost, "/api/v1/config:validate", body, "application/json")
}

// Reset restores every setting to its default.
func (c *Client) Reset() (*Response, error) {
	return c.do(http.MethodDelete, "/api/v1/config", nil, "")
}

// Upgrade asks KubeSolo to upgrade itself. It returns once the upgrade has been
// accepted; follow it with Status.
func (c *Client) Upgrade(req upgrade.Request) (*upgrade.Accepted, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	var out upgrade.Accepted
	return &out, c.doJSON(http.MethodPost, "/api/v1/upgrade", body, "application/json", &out)
}

// Rollback asks KubeSolo to restore the version before the last upgrade.
func (c *Client) Rollback(healthTimeoutSeconds int) (*upgrade.Accepted, error) {
	body, err := json.Marshal(rollbackRequest{HealthTimeoutSeconds: healthTimeoutSeconds})
	if err != nil {
		return nil, err
	}
	var out upgrade.Accepted
	return &out, c.doJSON(http.MethodPost, "/api/v1/rollback", body, "application/json", &out)
}

// Status reads the running version, health and upgrade record.
func (c *Client) Status() (*upgrade.Status, error) {
	var out upgrade.Status
	return &out, c.doJSON(http.MethodGet, "/api/v1/status", nil, "", &out)
}

func (c *Client) do(method, path string, body []byte, contentType string) (*Response, error) {
	var out Response
	if err := c.doJSON(method, path, body, contentType, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// doJSON sends a request and decodes a successful response into out.
func (c *Client) doJSON(method, path string, body []byte, contentType string, out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, "http://localhost"+path, reader)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("configuration API at %s: %w", c.socketPath, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode >= 300 {
		// The server's own message is far more useful than the status code, so
		// it is surfaced rather than replaced.
		var failure ErrorResponse
		if err := json.Unmarshal(raw, &failure); err == nil && failure.Error != "" {
			if failure.Field != "" {
				return &APIError{Status: resp.StatusCode, Message: failure.Field + ": " + failure.Error}
			}
			return &APIError{Status: resp.StatusCode, Message: failure.Error}
		}
		return &APIError{Status: resp.StatusCode, Message: "configuration API returned " + resp.Status}
	}

	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("could not read the response from the configuration API: %w", err)
	}
	return nil
}

// APIError is a request the API answered with an error status.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return e.Message }
