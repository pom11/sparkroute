// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package sparkrun

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testDaemonReady = `{"state":"ready","workload_id":"work","adopted":true,"observed_at":123.5,"endpoint":{"workload_id":"work","owner":"sparkroute","recipe_revision":"rev","actual_models":["model"],"healthy":true,"native_protocols":["openai"]},"workload":{"workload_id":"work","owner":"sparkroute","owned":true,"recipe_revision":"rev"}}`

const testDaemonJob = "0123456789abcdef0123456789abcdef"

func daemonConfig(t *testing.T, cfg DaemonConnection) string {
	t.Helper()
	if cfg.Manager == "" {
		cfg.Manager = "sparkroute"
	}
	if cfg.Protocol == 0 {
		cfg.Protocol = 1
	}
	if cfg.Binding == "" {
		cfg.Binding = "controller"
	}
	if cfg.Profile == "" {
		cfg.Profile = "sparkrun"
	}
	path := filepath.Join(t.TempDir(), "connection.json")
	writeDaemonConfig(t, path, cfg)
	return path
}
func writeDaemonConfig(t *testing.T, path string, cfg DaemonConnection) {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func daemonHandler(operation http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/info" {
			fmt.Fprint(w, `{"protocol":1,"binding":"controller","profile":"sparkrun","manager":"sparkroute","runtime":{"version":"0.4.0"}}`)
			return
		}
		if r.URL.Path == "/v1/ops" {
			fmt.Fprint(w, `[{"name":"plugins.sparkroute.endpoints.discover","version":1},{"name":"workloads.ensure_ready","version":1},{"name":"workloads.stop","version":1},{"name":"workloads.inventory","version":1},{"name":"catalog.recipe","version":1},{"name":"catalog.registries","version":1},{"name":"catalog.refresh","version":1}]`)
			return
		}
		operation(w, r)
	}
}
func testDaemonClient(t *testing.T, handler http.Handler) (*Client, string) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	path := daemonConfig(t, DaemonConnection{URL: server.URL, Token: "token"})
	client, err := NewDaemonClient(path)
	if err != nil {
		t.Fatal(err)
	}
	return client, path
}
func TestDaemonQueriesValidateBindingAndOperation(t *testing.T) {
	for _, wrong := range []string{"binding", "version"} {
		t.Run(wrong, func(t *testing.T) {
			var posts atomic.Int32
			base := daemonHandler(func(w http.ResponseWriter, r *http.Request) { posts.Add(1); fmt.Fprint(w, `{"endpoints":[]}`) })
			client, _ := testDaemonClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if wrong == "binding" && r.URL.Path == "/v1/info" {
					fmt.Fprint(w, `{"protocol":1,"binding":"other","profile":"sparkrun"}`)
					return
				}
				if wrong == "version" && r.URL.Path == "/v1/ops" {
					fmt.Fprint(w, `[{"name":"plugins.sparkroute.endpoints.discover","version":2}]`)
					return
				}
				base(w, r)
			}))
			if _, err := client.Discover(context.Background(), nil); err == nil {
				t.Fatal("accepted incompatible daemon")
			}
			if posts.Load() != 0 {
				t.Fatal("submitted before validating controller")
			}
		})
	}
}
func TestDaemonTokenRotationAndCatalogClientWithoutCapabilities(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	os.WriteFile(tokenFile, []byte("first"), 0600)
	var token atomic.Value
	token.Store("first")
	base := daemonHandler(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `[]`) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token.Load().(string) {
			w.WriteHeader(403)
			return
		}
		base(w, r)
	}))
	defer server.Close()
	client, err := NewDaemonClient(daemonConfig(t, DaemonConnection{URL: server.URL, TokenFile: tokenFile}))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"first", "rotated"} {
		token.Store(value)
		os.WriteFile(tokenFile, []byte(value), 0600)
		var result struct {
			Registries []any `json:"registries"`
		}
		if err := client.invokeDaemon(context.Background(), bridgeRequest{Operation: "catalog_registries"}, &result); err != nil {
			t.Fatal(err)
		}
	}
}
func TestDaemonSSEProgressReplayGapAndNewIntent(t *testing.T) {
	var mu sync.Mutex
	keys := []string{}
	polls := 0
	streams := 0
	handler := daemonHandler(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "endpoints.metadata"):
			fmt.Fprint(w, `{"model_metadata":{}}`)
		case r.Method == "POST":
			keys = append(keys, r.Header.Get("Idempotency-Key"))
			w.WriteHeader(202)
			fmt.Fprintf(w, `{"job_id":%q}`, testDaemonJob)
		case strings.HasSuffix(r.URL.Path, "/events"):
			streams++
			if streams == 1 {
				w.WriteHeader(410)
				return
			}
			if r.Header.Get("Last-Event-ID") != "0" {
				t.Error("gap did not reset cursor")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "id: 7\ndata: {\"id\":7,\"type\":\"progress\",\"stage\":\"launch\",\"workload_id\":\"work\"}\n\n")
		default:
			polls++
			state := "running"
			result := "null"
			if streams >= 2 {
				state = "succeeded"
				result = testDaemonReady
			}
			fmt.Fprintf(w, `{"id":%q,"state":%q,"result":%s}`, testDaemonJob, state, result)
		}
	})
	client, _ := testDaemonClient(t, handler)
	seen := false
	client.SetProgressObserver(func(_ Binding, op Operation) {
		if op.Phase == "launch" && op.WorkloadID == "work" {
			seen = true
		}
	})
	result, err := client.EnsureReady(context.Background(), Binding{Recipe: "recipe"}, time.Second)
	if err != nil || !seen || result.Endpoint.ObservedAt != 123.5 {
		t.Fatalf("result=%+v err=%v progress=%v", result, err, seen)
	}
	_, err = client.EnsureReady(context.Background(), Binding{Recipe: "recipe"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] == "" || keys[0] == keys[1] {
		t.Fatalf("new intent did not get new key: %v", keys)
	}
}
func TestDaemonDisconnectKeepsIntentAcrossClientRestart(t *testing.T) {
	var mu sync.Mutex
	keys := []string{}
	ready := false
	handler := daemonHandler(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "endpoints.metadata"):
			fmt.Fprint(w, `{"model_metadata":{}}`)
		case r.Method == "POST":
			keys = append(keys, r.Header.Get("Idempotency-Key"))
			w.WriteHeader(202)
			fmt.Fprintf(w, `{"job_id":%q}`, testDaemonJob)
		case strings.HasSuffix(r.URL.Path, "/events"):
			w.Header().Set("Content-Type", "text/event-stream")
		default:
			state := "running"
			result := "null"
			if ready {
				state = "succeeded"
				result = testDaemonReady
			}
			fmt.Fprintf(w, `{"id":%q,"state":%q,"result":%s}`, testDaemonJob, state, result)
		}
	})
	client, path := testDaemonClient(t, handler)
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
	defer cancel()
	if _, err := client.EnsureReady(ctx, Binding{Recipe: "recipe"}, time.Second); err == nil {
		t.Fatal("expected interrupted wait")
	}
	mu.Lock()
	ready = true
	mu.Unlock()
	client, err := NewDaemonClient(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.EnsureReady(context.Background(), Binding{Recipe: "recipe"}, time.Second); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] {
		t.Fatalf("uncertain intent changed: %v", keys)
	}
}
func TestDaemonConcurrentClientsPersistOneAction(t *testing.T) {
	client, path := testDaemonClient(t, daemonHandler(func(w http.ResponseWriter, r *http.Request) {}))
	other, err := NewDaemonClient(path)
	if err != nil {
		t.Fatal(err)
	}
	keys := make(chan string, 2)
	errors := make(chan error, 2)
	for _, c := range []*Client{client, other} {
		go func(c *Client) {
			a, _, err := c.daemon.action("op", map[string]any{"binding": "same"})
			keys <- a.Key
			errors <- err
		}(c)
	}
	a, b := <-keys, <-keys
	if a == "" || a != b {
		t.Fatalf("competing keys %q %q", a, b)
	}
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
}
func TestDaemonPrivateConnectionAndChangedBinding(t *testing.T) {
	client, path := testDaemonClient(t, daemonHandler(func(w http.ResponseWriter, r *http.Request) {}))
	raw, _ := os.ReadFile(path)
	var cfg DaemonConnection
	json.Unmarshal(raw, &cfg)
	cfg.Binding = "different"
	writeDaemonConfig(t, path, cfg)
	if _, _, _, err := client.daemon.connection(); err == nil {
		t.Fatal("accepted changed controller")
	}
	if runtime.GOOS != "windows" {
		os.Chmod(path, 0644)
		if _, err := NewDaemonClient(path); err == nil {
			t.Fatal("accepted public credential file")
		}
	}
}
func TestDaemonUnixSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket client exercised on POSIX")
	}
	// Keep the socket path below sockaddr_un's small platform limit.
	dir, err := os.MkdirTemp("", "sr-uds-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "api.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: daemonHandler(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"endpoints":[]}`) })}
	go server.Serve(listener)
	defer server.Close()
	client, err := NewDaemonClient(daemonConfig(t, DaemonConnection{Socket: path}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Discover(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestDaemonTLSAndPermissionFailures(t *testing.T) {
	server := httptest.NewTLSServer(daemonHandler(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"detail":"insufficient permission"}`)
	}))
	defer server.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600)
	client, err := NewDaemonClient(daemonConfig(t, DaemonConnection{URL: server.URL, Token: "test", CAFile: ca}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Capabilities(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Discover(context.Background(), nil); err == nil {
		t.Fatal("permission failure was ignored")
	}
}

func TestDaemonCatalogRecipeProjection(t *testing.T) {
	raw := json.RawMessage(`{"name":"recipe","source_kind":"imported","source_path":"opaque-controller-path","model":"model","runtime":"vllm","plugin_items":{"sparkroute":{"capabilities":["chat"],"request_profiles":{}}},"defaults":{"max_model_len":1024},"normalized_overrides":{"max_model_len":4096},"metadata":{"parameters_b":7,"quantization":"fp8"}}`)
	projected, err := projectCoreRecipe(raw)
	if err != nil {
		t.Fatal(err)
	}
	var details RecipeDetails
	if err = decodeStrict(projected, &details); err != nil {
		t.Fatal(err)
	}
	if details.ProfileSourceKind != "snapshot" || details.ModelMetadata == nil {
		t.Fatalf("bad details %+v", details)
	}
	if bytes.Contains(projected, []byte("plugin_items")) || bytes.Contains(projected, []byte("normalized_overrides")) {
		t.Fatal("unadapted core envelope")
	}
}

func TestDaemonLocalTCPDescriptorFollowsRestartAndRejectsMismatchedCredentials(t *testing.T) {
	var token atomic.Value
	token.Store("first")
	base := daemonHandler(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `[]`) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token.Load().(string) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		base(w, r)
	}))
	defer server.Close()
	root := t.TempDir()
	statePath, credentialPath := filepath.Join(root, "state.json"), filepath.Join(root, "credentials.json")
	state := DaemonConnection{Protocol: 1, Binding: "controller", Profile: "sparkrun", URL: server.URL,
		Process: map[string]any{"pid": 1, "created": "first"}}
	credentials := state
	credentials.Token = "first"
	writeDaemonConfig(t, statePath, state)
	writeDaemonConfig(t, credentialPath, credentials)
	client, err := NewDaemonClient(daemonConfig(t, DaemonConnection{StateFile: statePath, ConnectionFile: credentialPath}))
	if err != nil {
		t.Fatal(err)
	}
	call := func() error {
		var result struct {
			Registries []any `json:"registries"`
		}
		return client.invokeDaemon(context.Background(), bridgeRequest{Operation: "catalog_registries"}, &result)
	}
	if err := call(); err != nil {
		t.Fatal(err)
	}
	state.Process = map[string]any{"pid": 2, "created": "second"}
	writeDaemonConfig(t, statePath, state)
	if err := call(); err == nil {
		t.Fatal("accepted old credentials for a replacement daemon")
	}
	credentials = state
	credentials.Token = "rotated"
	token.Store("rotated")
	writeDaemonConfig(t, credentialPath, credentials)
	if err := call(); err != nil {
		t.Fatal(err)
	}
}

func TestDaemonNamedRegistryActions(t *testing.T) {
	actions := []string{"add", "remove", "enable", "disable", "trust", "untrust"}
	for _, action := range actions {
		t.Run(action, func(t *testing.T) {
			var submitted atomic.Int32
			base := daemonHandler(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/ops/catalog.registries."+action {
					t.Errorf("wrong route: %s", r.URL.Path)
				}
				var args map[string]any
				if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
					t.Error(err)
				}
				if args["name"] != "fixture" {
					t.Errorf("lost registry identity: %v", args)
				}
				if _, exists := args["action"]; exists {
					t.Error("sent retired action field")
				}
				if action == "add" && args["url"] != "https://example.invalid/recipes.git" {
					t.Error("lost URL")
				}
				if action == "trust" && args["acknowledge_trust"] != true {
					t.Error("lost trust acknowledgement")
				}
				if action != "add" {
					if _, exists := args["url"]; exists {
						t.Error("irrelevant URL argument")
					}
				}
				submitted.Add(1)
				fmt.Fprint(w, `{"registries":[]}`)
			})
			client, _ := testDaemonClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/ops" {
					fmt.Fprintf(w, `[{"name":%q,"version":1}]`, "catalog.registries."+action)
					return
				}
				base(w, r)
			}))
			args := map[string]any{"action": action, "name": "fixture", "url": "https://example.invalid/recipes.git", "acknowledge_trust": true}
			var result struct {
				Registries []any `json:"registries"`
			}
			if err := client.invokeDaemon(context.Background(), bridgeRequest{Operation: "catalog_registry", Arguments: args}, &result); err != nil {
				t.Fatal(err)
			}
			if submitted.Load() != 1 {
				t.Fatal("expected one mutation")
			}
		})
	}
	if _, _, err := daemonRegistryAction(map[string]any{"action": "anything"}); err == nil {
		t.Fatal("accepted unknown action")
	}
}
