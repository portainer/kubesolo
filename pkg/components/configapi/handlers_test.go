package configapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portainer/kubesolo/internal/config"
	"github.com/portainer/kubesolo/types"
)

// api brings up the service against a fresh configuration file and returns a
// client plus the file's path.
func api(t *testing.T, seed func(*types.Config)) (*http.Client, string) {
	t.Helper()

	dir := shortTempDir(t)
	configPath := filepath.Join(dir, "config.yaml")

	cfg := config.Defaults()
	if seed != nil {
		seed(cfg)
	}
	if err := config.Write(configPath, cfg); err != nil {
		t.Fatal(err)
	}

	socket := filepath.Join(dir, "c.sock")
	ctx, cancel := context.WithCancel(context.Background())
	svc := NewService(ctx, cancel, make(chan struct{}), Options{
		SocketPath: socket,
		ConfigPath: configPath,
		Host:       config.Host{NumCPU: 4, GOARCH: "amd64"},
	})

	done := make(chan error, 1)
	go func() { done <- svc.Run() }()
	<-svc.readyCh
	t.Cleanup(func() { cancel(); <-done })

	return unixClient(socket), configPath
}

func do(t *testing.T, client *http.Client, method, path string, body any, headers map[string]string) (*http.Response, []byte) {
	t.Helper()

	var reader io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequest(method, "http://localhost"+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, raw
}

func decode(t *testing.T, raw []byte) Response {
	t.Helper()
	var out Response
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Response is not valid JSON: %v\n%s", err, raw)
	}
	return out
}

func TestGetReturnsTheConfiguration(t *testing.T) {
	client, _ := api(t, func(c *types.Config) { c.Network.NodeIP = "10.0.0.5" })

	resp, raw := do(t, client, "GET", "/api/v1/config", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, raw)
	}
	if got := decode(t, raw).Config.Network.NodeIP; got != "10.0.0.5" {
		t.Errorf("nodeIP = %q", got)
	}
	if resp.Header.Get("ETag") == "" {
		t.Error("no ETag header; callers cannot use If-Match without one")
	}
}

// TestSecretsAreRedactedInTheResponseBytes checks the bytes on the wire, not the
// decoded struct: a value that survives anywhere in the payload has leaked.
func TestSecretsAreRedactedInTheResponseBytes(t *testing.T) {
	const secret = "super-secret-edge-key"
	client, _ := api(t, func(c *types.Config) { c.Portainer.EdgeKey = secret })

	_, raw := do(t, client, "GET", "/api/v1/config", nil, nil)
	if bytes.Contains(raw, []byte(secret)) {
		t.Errorf("the edge key appears in the Response: %s", raw)
	}
	if got := decode(t, raw).Config.Portainer.EdgeKey; got != redacted {
		t.Errorf("edgeKey = %q, want %q", got, redacted)
	}

	_, raw = do(t, client, "GET", "/api/v1/config?showSecrets=true", nil, nil)
	if !bytes.Contains(raw, []byte(secret)) {
		t.Error("showSecrets=true should return the real value")
	}
}

// TestReadModifyWriteCannotBlankASecret is the trap redaction creates: a client
// reads a redacted document, changes one field and sends it back. Without this
// the credential would be overwritten with the placeholder.
func TestReadModifyWriteCannotBlankASecret(t *testing.T) {
	const secret = "super-secret-edge-key"
	client, configPath := api(t, func(c *types.Config) { c.Portainer.EdgeKey = secret })

	_, raw := do(t, client, "GET", "/api/v1/config", nil, nil)
	document := decode(t, raw).Config
	document.Network.NodeIP = "10.0.0.9"

	resp, raw := do(t, client, "PUT", "/api/v1/config", document, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "portainer.edgeKey") {
		t.Errorf("error should name the setting, got: %s", raw)
	}

	stored := config.Defaults()
	if _, _, err := config.Read(configPath, stored); err != nil {
		t.Fatal(err)
	}
	if stored.Portainer.EdgeKey != secret {
		t.Errorf("the stored credential was damaged: %q", stored.Portainer.EdgeKey)
	}
}

func TestPatchAppliesOneSetting(t *testing.T) {
	client, _ := api(t, func(c *types.Config) { c.Network.NodeIP = "10.0.0.5" })

	resp, raw := do(t, client, "PATCH", "/api/v1/config",
		`{"network":{"mtu":1400}}`,
		map[string]string{"Content-Type": "application/merge-patch+json"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, raw)
	}

	got := decode(t, raw)
	if got.Config.Network.MTU != 1400 {
		t.Errorf("mtu = %d", got.Config.Network.MTU)
	}
	if got.Config.Network.NodeIP != "10.0.0.5" {
		t.Errorf("patch must leave unmentioned settings alone, nodeIP = %q", got.Config.Network.NodeIP)
	}
	if !got.RestartRequired || len(got.Changed) != 1 || got.Changed[0] != "network.mtu" {
		t.Errorf("changed = %v, restartRequired = %v", got.Changed, got.RestartRequired)
	}
}

// TestPatchNullRestoresTheDefault is the RFC 7386 rule that distinguishes a
// merge patch from a partial decode: null removes the setting, and a removed
// setting falls back to its default rather than to its zero value.
func TestPatchNullRestoresTheDefault(t *testing.T) {
	client, _ := api(t, func(c *types.Config) { c.D2K.Namespace = "workloads" })

	_, raw := do(t, client, "PATCH", "/api/v1/config",
		`{"d2k":{"namespace":null}}`,
		map[string]string{"Content-Type": "application/merge-patch+json"})

	got := decode(t, raw).Config.D2K.Namespace
	if got != types.DefaultD2KNamespace {
		t.Errorf("namespace = %q, want the default %q — not the zero value", got, types.DefaultD2KNamespace)
	}
}

// TestPutReplacesRatherThanMerges is what separates PUT from PATCH: a setting
// the caller omitted is being removed.
func TestPutReplacesRatherThanMerges(t *testing.T) {
	client, _ := api(t, func(c *types.Config) {
		c.Network.NodeIP = "10.0.0.5"
		c.D2K.Namespace = "workloads"
	})

	_, raw := do(t, client, "PUT", "/api/v1/config", `{"network":{"mtu":1400}}`, nil)

	got := decode(t, raw).Config
	if got.Network.MTU != 1400 {
		t.Errorf("mtu = %d", got.Network.MTU)
	}
	if got.Network.NodeIP != "" {
		t.Errorf("PUT must drop omitted settings, nodeIP = %q", got.Network.NodeIP)
	}
	if got.D2K.Namespace != types.DefaultD2KNamespace {
		t.Errorf("omitted settings return to their default, namespace = %q", got.D2K.Namespace)
	}
}

func TestDeleteResetsToDefaults(t *testing.T) {
	client, _ := api(t, func(c *types.Config) {
		c.Path = "/opt/kubesolo"
		c.Network.NodeIP = "10.0.0.5"
		c.Logging.Debug = true
	})

	resp, raw := do(t, client, "DELETE", "/api/v1/config", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, raw)
	}

	got := decode(t, raw).Config
	if got.Network.NodeIP != "" || got.Logging.Debug {
		t.Errorf("settings were not reset: %+v", got)
	}
	if got.Path != "/opt/kubesolo" {
		t.Errorf("path is immutable and must survive a reset, got %q", got.Path)
	}
}

func TestImmutableSettingIsRefused(t *testing.T) {
	client, configPath := api(t, nil)

	resp, raw := do(t, client, "PATCH", "/api/v1/config",
		`{"path":"/somewhere/else"}`,
		map[string]string{"Content-Type": "application/merge-patch+json"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", resp.StatusCode, raw)
	}

	var apiErr ErrorResponse
	if err := json.Unmarshal(raw, &apiErr); err != nil {
		t.Fatal(err)
	}
	if apiErr.Field != "path" {
		t.Errorf("error should name the setting, got %+v", apiErr)
	}

	assertUnchanged(t, configPath, func(c *types.Config) bool { return c.Path == types.DefaultBasePath })
}

func TestInvalidConfigurationIsRefused(t *testing.T) {
	for _, tc := range []struct{ name, patch string }{
		{"d2k without the load balancer", `{"d2k":{"enabled":true},"network":{"loadBalancer":{"enabled":false}}}`},
		{"unknown cpu manager policy", `{"kubernetes":{"kubelet":{"cpuManager":{"policy":"dynamic"}}}}`},
		{"unsupported reserved resource", `{"kubernetes":{"kubelet":{"systemReserved":{"gpu":"1"}}}}`},
		{"bad image reference", `{"portainer":{"image":"NOT A REF"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, configPath := api(t, func(c *types.Config) { c.Network.NodeIP = "10.0.0.1" })

			resp, raw := do(t, client, "PATCH", "/api/v1/config", tc.patch,
				map[string]string{"Content-Type": "application/merge-patch+json"})
			if resp.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422: %s", resp.StatusCode, raw)
			}

			assertUnchanged(t, configPath, func(c *types.Config) bool { return c.Network.NodeIP == "10.0.0.1" })
		})
	}
}

func TestMalformedRequests(t *testing.T) {
	for _, tc := range []struct {
		name    string
		method  string
		body    string
		headers map[string]string
		want    int
	}{
		{"patch is not JSON", "PATCH", "{not json", map[string]string{"Content-Type": "application/merge-patch+json"}, http.StatusBadRequest},
		{"put is not JSON", "PUT", "{not json", nil, http.StatusBadRequest},
		{"wrong patch content type", "PATCH", `{}`, map[string]string{"Content-Type": "application/xml"}, http.StatusUnsupportedMediaType},
		{"wrongly typed value", "PUT", `{"network":{"mtu":"tall"}}`, nil, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := api(t, nil)
			resp, raw := do(t, client, tc.method, "/api/v1/config", tc.body, tc.headers)
			if resp.StatusCode != tc.want {
				t.Errorf("status = %d, want %d: %s", resp.StatusCode, tc.want, raw)
			}
		})
	}
}

func TestIfMatchPrecondition(t *testing.T) {
	client, _ := api(t, nil)

	resp, _ := do(t, client, "GET", "/api/v1/config", nil, nil)
	etag := resp.Header.Get("ETag")

	t.Run("current etag is accepted", func(t *testing.T) {
		resp, raw := do(t, client, "PATCH", "/api/v1/config", `{"network":{"mtu":1400}}`,
			map[string]string{"Content-Type": "application/merge-patch+json", "If-Match": etag})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d: %s", resp.StatusCode, raw)
		}
	})

	t.Run("stale etag is refused", func(t *testing.T) {
		// The write above moved the document on, so the original tag is stale.
		resp, raw := do(t, client, "PATCH", "/api/v1/config", `{"network":{"mtu":1500}}`,
			map[string]string{"Content-Type": "application/merge-patch+json", "If-Match": etag})
		if resp.StatusCode != http.StatusPreconditionFailed {
			t.Fatalf("status = %d, want 412: %s", resp.StatusCode, raw)
		}
	})
}

// TestValidateWritesNothing — a dry run must not touch the file.
func TestValidateWritesNothing(t *testing.T) {
	client, configPath := api(t, func(c *types.Config) { c.Network.NodeIP = "10.0.0.1" })

	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	resp, raw := do(t, client, "POST", "/api/v1/config:validate",
		`{"network":{"nodeIP":"10.0.0.9"}}`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, raw)
	}

	got := decode(t, raw)
	if len(got.Changed) == 0 {
		t.Error("a dry run should report what it would change")
	}

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the dry run modified the configuration file")
	}
}

func TestValidateRejectsAnInvalidCandidate(t *testing.T) {
	client, _ := api(t, nil)

	resp, raw := do(t, client, "POST", "/api/v1/config:validate",
		`{"d2k":{"enabled":true},"network":{"loadBalancer":{"enabled":false}}}`, nil)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", resp.StatusCode, raw)
	}
}

func TestSchemaCoversEverySetting(t *testing.T) {
	client, _ := api(t, nil)

	_, raw := do(t, client, "GET", "/api/v1/config/schema", nil, nil)

	var doc struct {
		Settings []config.Descriptor `json:"settings"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Settings) != len(config.Registry()) {
		t.Errorf("schema has %d settings, registry has %d", len(doc.Settings), len(config.Registry()))
	}
}

// TestConcurrentPatchesAllApply covers the read-modify-write race: without
// serialisation, two writers can both read the same document and one change is
// lost.
func TestConcurrentPatchesAllApply(t *testing.T) {
	client, configPath := api(t, nil)

	patches := []string{
		`{"network":{"mtu":1400}}`,
		`{"logging":{"debug":true}}`,
		`{"d2k":{"namespace":"one"}}`,
		`{"storage":{"dbWALRepair":true}}`,
		`{"metrics":{"enabled":true}}`,
	}

	done := make(chan struct{})
	for _, patch := range patches {
		go func(p string) {
			defer func() { done <- struct{}{} }()
			do(t, client, "PATCH", "/api/v1/config", p,
				map[string]string{"Content-Type": "application/merge-patch+json"})
		}(patch)
	}
	for range patches {
		<-done
	}

	final := config.Defaults()
	if _, _, err := config.Read(configPath, final); err != nil {
		t.Fatal(err)
	}
	if final.Network.MTU != 1400 || !final.Logging.Debug || final.D2K.Namespace != "one" ||
		!final.Storage.DBWALRepair || !final.Metrics.Enabled {
		t.Errorf("a concurrent write was lost: %+v", final)
	}
}

func assertUnchanged(t *testing.T, configPath string, ok func(*types.Config) bool) {
	t.Helper()
	stored := config.Defaults()
	if _, _, err := config.Read(configPath, stored); err != nil {
		t.Fatal(err)
	}
	if !ok(stored) {
		t.Errorf("the configuration file was modified by a rejected request: %+v", stored)
	}
}
