// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package sparkrun

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sparksq/sparkroute/internal/privatepath"
)

const DaemonProtocolVersion = 1

// DaemonConnection is private connection configuration, never gateway exports.
// StateFile/ConnectionFile follow a local daemon across restarts/token rotation.
// An explicit remote connection instead sets URL and TokenFile (or Token).
type DaemonConnection struct {
	Protocol        int            `json:"protocol"`
	Manager         string         `json:"manager"`
	Binding         string         `json:"binding"`
	Profile         string         `json:"profile"`
	Socket          string         `json:"socket"`
	URL             string         `json:"url"`
	Token           string         `json:"token"`
	TokenFile       string         `json:"token_file"`
	CAFile          string         `json:"ca_file"`
	StateFile       string         `json:"state_file"`
	ConnectionFile  string         `json:"connection_file"`
	ActionDirectory string         `json:"action_directory"`
	Process         map[string]any `json:"process"`
}

type daemonTransport struct {
	path            string
	expectedBinding string
	expectedProfile string
	mu              sync.Mutex
	http            *http.Client
	origin          string
	target          string
}

type daemonJob struct {
	ID      string          `json:"id"`
	State   string          `json:"state"`
	Updated float64         `json:"updated"`
	Result  json.RawMessage `json:"result"`
	Error   *daemonFailure  `json:"error"`
}

type daemonFailure struct {
	Detail string `json:"detail"`
	Error  struct {
		Type      string `json:"type"`
		Retryable bool   `json:"retryable"`
		Attrs     struct {
			Code string `json:"code"`
		} `json:"attrs"`
	} `json:"error"`
}

func (f *daemonFailure) bridgeError() *BridgeError {
	code := f.Error.Attrs.Code
	if code == "" {
		code = f.Error.Type
	}
	if code == "" {
		code = "daemon_error"
	}
	return &BridgeError{Code: code, Message: f.Detail, Retryable: f.Error.Retryable}
}

// NewDaemonClient uses direct HTTP/SSE and never invokes the bridge executable.
func NewDaemonClient(connectionFile string) (*Client, error) {
	d := &daemonTransport{path: connectionFile}
	_, _, config, err := d.connection()
	if err != nil {
		return nil, err
	}
	d.expectedBinding, d.expectedProfile = config.Binding, config.Profile
	return &Client{daemon: d}, nil
}

func privateRead(path string) ([]byte, error) {
	if err := checkDaemonAncestors(path); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("daemon connection must be a regular file")
	}
	if err := privatepath.Check(path, info.Mode()); err != nil {
		return nil, err
	}
	if err := checkDaemonFileOwner(info); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("daemon connection changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("daemon connection exceeds size limit")
	}
	return data, err
}

func (d *daemonTransport) configuration() (DaemonConnection, error) {
	var config DaemonConnection
	raw, err := privateRead(d.path)
	if err != nil {
		return config, err
	}
	if err = json.Unmarshal(raw, &config); err != nil {
		return config, fmt.Errorf("invalid daemon connection configuration")
	}
	if config.Protocol != DaemonProtocolVersion || config.Binding == "" || config.Profile == "" || config.Manager != "sparkroute" {
		return config, fmt.Errorf("daemon connection requires protocol, binding, profile and manager sparkroute")
	}
	if d.expectedBinding != "" && (config.Binding != d.expectedBinding || config.Profile != d.expectedProfile) {
		return config, fmt.Errorf("daemon connection changed controller binding")
	}
	if config.StateFile != "" {
		var state DaemonConnection
		raw, err = privateRead(config.StateFile)
		if err != nil {
			return config, err
		}
		if err = json.Unmarshal(raw, &state); err != nil {
			return config, err
		}
		if state.Binding != config.Binding || state.Profile != config.Profile || state.Protocol != config.Protocol {
			return config, fmt.Errorf("daemon state belongs to another controller")
		}
		config.Socket, config.URL = state.Socket, state.URL
		if state.Socket == "" && config.TokenFile == "" {
			var credentials DaemonConnection
			raw, err = privateRead(config.ConnectionFile)
			if err != nil {
				return config, err
			}
			if err = json.Unmarshal(raw, &credentials); err != nil {
				return config, err
			}
			if credentials.Binding != state.Binding || credentials.Profile != state.Profile ||
				len(state.Process) == 0 || !reflect.DeepEqual(credentials.Process, state.Process) {
				return config, fmt.Errorf("daemon credentials do not match the serving process")
			}
			config.URL, config.Token = credentials.URL, credentials.Token
		}
	}
	if config.TokenFile != "" {
		raw, err = privateRead(config.TokenFile)
		if err != nil {
			return config, err
		}
		config.Token = strings.TrimSpace(string(raw))
	}
	if len(config.Token) > 4096 || strings.IndexFunc(config.Token, func(r rune) bool { return r <= 0x20 || r > 0x7e }) >= 0 {
		return config, fmt.Errorf("invalid daemon token")
	}
	if config.ActionDirectory == "" {
		config.ActionDirectory = filepath.Join(filepath.Dir(d.path), "daemon-actions")
	}
	return config, nil
}

func (d *daemonTransport) connection() (*http.Client, string, DaemonConnection, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	config, err := d.configuration()
	if err != nil {
		return nil, "", config, err
	}
	origin := config.URL
	if config.Socket != "" {
		origin = "http://localhost"
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, "", config, fmt.Errorf("expected a daemon HTTP(S) origin or Unix socket")
	}
	if config.Socket == "" && config.Token == "" {
		return nil, "", config, fmt.Errorf("TCP daemon requires a token")
	}
	target := origin + "\x00" + config.Socket + "\x00" + config.CAFile
	if d.http == nil || target != d.target {
		transport := &http.Transport{Proxy: nil, MaxIdleConns: 16, MaxIdleConnsPerHost: 8,
			IdleConnTimeout: 90 * time.Second, ResponseHeaderTimeout: 35 * time.Second}
		if config.Socket != "" {
			socket := config.Socket
			transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "unix", socket)
			}
		}
		if config.CAFile != "" {
			pem, err := os.ReadFile(config.CAFile)
			if err != nil {
				return nil, "", config, err
			}
			roots, err := x509.SystemCertPool()
			if err != nil {
				roots = x509.NewCertPool()
			}
			if !roots.AppendCertsFromPEM(pem) {
				return nil, "", config, fmt.Errorf("invalid daemon CA bundle")
			}
			transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
		}
		if d.http != nil {
			d.http.CloseIdleConnections()
		}
		d.http = &http.Client{Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return fmt.Errorf("daemon redirects are not allowed")
		}}
		d.origin, d.target = strings.TrimRight(origin, "/"), target
	}
	return d.http, d.origin, config, nil
}

func (d *daemonTransport) request(ctx context.Context, method, path string, body any, key string) (*http.Response, error) {
	client, origin, config, err := d.connection()
	if err != nil {
		return nil, err
	}
	var input io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		input = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, origin+path, input)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Sparkrun-Client", "sparkroute")
	if config.Token != "" {
		req.Header.Set("Authorization", "Bearer "+config.Token)
	}
	if key != "" {
		if method == http.MethodGet {
			req.Header.Set("Last-Event-ID", key)
		} else {
			req.Header.Set("Idempotency-Key", key)
		}
	}
	return client.Do(req)
}

func (d *daemonTransport) json(ctx context.Context, method, path string, body any, key string, value any) (int, error) {
	response, err := d.request(ctx, method, path, body, key)
	if err != nil {
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, MaxBridgeResponseBytes+1))
	if err != nil {
		return response.StatusCode, err
	}
	if len(raw) > MaxBridgeResponseBytes {
		return response.StatusCode, fmt.Errorf("daemon response exceeds limit")
	}
	if response.StatusCode >= 400 {
		var failure daemonFailure
		if json.Unmarshal(raw, &failure) != nil {
			return response.StatusCode, fmt.Errorf("daemon returned HTTP %d", response.StatusCode)
		}
		return response.StatusCode, failure.bridgeError()
	}
	return response.StatusCode, json.Unmarshal(raw, value)
}

func (c *Client) daemonCapabilities(ctx context.Context) (Capabilities, error) {
	required := map[string]string{"discover": "plugins.sparkroute.endpoints.discover", "ensure_ready": "workloads.ensure_ready",
		"stop": "workloads.stop", "workloads": "workloads.inventory", "catalog_resolve": "catalog.recipe"}
	return c.validateDaemon(ctx, required)
}

// Every consumer validates the server and the exact operation it uses, including
// catalog/profile clients that never construct a lifecycle controller.
func (c *Client) validateDaemon(ctx context.Context, required map[string]string) (Capabilities, error) {
	_, _, connection, err := c.daemon.connection()
	if err != nil {
		return Capabilities{}, err
	}
	var info struct {
		Protocol int    `json:"protocol"`
		Binding  string `json:"binding"`
		Profile  string `json:"profile"`
		Manager  string `json:"manager"`
		Restart  bool   `json:"restart_required"`
		Runtime  struct {
			Version string `json:"version"`
		} `json:"runtime"`
	}
	if _, err = c.daemon.json(ctx, "GET", "/v1/info", nil, "", &info); err != nil {
		return Capabilities{}, err
	}
	if info.Protocol != DaemonProtocolVersion || info.Binding != connection.Binding || info.Profile != connection.Profile || info.Manager != connection.Manager || info.Restart {
		return Capabilities{}, fmt.Errorf("daemon protocol, binding, profile, manager or runtime does not match")
	}
	var operations []struct {
		Name    string `json:"name"`
		Version int    `json:"version"`
	}
	if _, err = c.daemon.json(ctx, "GET", "/v1/ops", nil, "", &operations); err != nil {
		return Capabilities{}, err
	}
	versions := map[string]int{}
	for _, op := range operations {
		versions[op.Name] = op.Version
	}

	for _, name := range required {
		if versions[name] != 1 {
			return Capabilities{}, fmt.Errorf("required daemon operation %s version 1 is unavailable", name)
		}
	}
	names := make([]string, 0, len(required))
	for name := range required {
		names = append(names, name)
	}
	return Capabilities{ProtocolVersion: DaemonProtocolVersion, Transport: "daemon", Operations: names, SparkrunVersion: info.Runtime.Version}, nil
}

var daemonJobID = regexp.MustCompile(`^[a-f0-9]{32}$`)

type daemonAction struct {
	Key string `json:"key"`
	Job string `json:"job"`
}

// Persist before submission. A lost HTTP response or gateway restart reuses the
// same action; a completed action is removed so a later intention gets a new ID.
func (d *daemonTransport) action(operation string, arguments any) (daemonAction, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	config, err := d.configuration()
	if err != nil {
		return daemonAction{}, "", err
	}
	if err = os.MkdirAll(config.ActionDirectory, 0o700); err != nil {
		return daemonAction{}, "", err
	}
	stat, err := os.Lstat(config.ActionDirectory)
	if err != nil || !stat.IsDir() {
		return daemonAction{}, "", fmt.Errorf("invalid daemon action directory")
	}
	if err = privatepath.Check(config.ActionDirectory, stat.Mode()); err != nil {
		return daemonAction{}, "", err
	}
	unlock, err := lockDaemonActions(config.ActionDirectory)
	if err != nil {
		return daemonAction{}, "", err
	}
	defer unlock()
	input, _ := json.Marshal([]any{config.Binding, config.Manager, operation, arguments})
	digest := sha256.Sum256(input)
	path := filepath.Join(config.ActionDirectory, hex.EncodeToString(digest[:])+".json")
	if raw, err := privateRead(path); err == nil {
		var action daemonAction
		if err := json.Unmarshal(raw, &action); err != nil {
			return action, path, err
		}
		if action.Key == "" {
			return action, path, fmt.Errorf("invalid persisted daemon action")
		}
		return action, path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return daemonAction{}, path, err
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return daemonAction{}, path, err
	}
	action := daemonAction{Key: "sparkroute-" + hex.EncodeToString(random)}
	raw, _ := json.Marshal(action)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return daemonAction{}, path, err
	}
	_, err = file.Write(raw)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	return action, path, err
}

func (c *Client) submitDaemon(ctx context.Context, name string, args any, mutation bool) (json.RawMessage, string, string, error) {
	if _, err := c.validateDaemon(ctx, map[string]string{name: name}); err != nil {
		return nil, "", "", err
	}
	var action daemonAction
	var path string
	var err error
	if mutation {
		action, path, err = c.daemon.action(name, args)
		if err != nil {
			return nil, "", path, err
		}
	}
	var raw json.RawMessage
	// Only transport failures are retried, using the exact persisted key.
	var status int
	for attempt := 0; attempt < 2; attempt++ {
		status, err = c.daemon.json(ctx, "POST", "/v1/ops/"+url.PathEscape(name), args, action.Key, &raw)
		if err == nil || status != 0 || !mutation || ctx.Err() != nil {
			break
		}
	}
	if err != nil {
		return nil, "", path, err
	}
	if status != http.StatusAccepted {
		return raw, "", path, nil
	}
	var accepted struct {
		Job string `json:"job_id"`
	}
	if err = json.Unmarshal(raw, &accepted); err != nil || !daemonJobID.MatchString(accepted.Job) {
		return nil, "", path, fmt.Errorf("invalid daemon job acceptance")
	}
	if path != "" {
		action.Job = accepted.Job
		if err = updateDaemonAction(path, action); err != nil {
			return nil, accepted.Job, path, err
		}
	}
	return nil, accepted.Job, path, nil
}

func (d *daemonTransport) job(ctx context.Context, id string) (daemonJob, error) {
	var job daemonJob
	if !daemonJobID.MatchString(id) {
		return job, fmt.Errorf("invalid daemon job ID")
	}
	_, err := d.json(ctx, "GET", "/v1/jobs/"+url.PathEscape(id), nil, "", &job)
	return job, err
}

func operationFromJob(job daemonJob) Operation {
	result := Operation{ID: job.ID, State: job.State, Phase: job.State, UpdatedAt: job.Updated, Result: job.Result}
	if job.Error != nil {
		result.Error = job.Error.bridgeError()
	}
	switch job.State {
	case "queued", "starting", "waiting_input", "cancelling":
		result.State = "running"
	case "cancelled", "lost":
		result.State = "failed"
		if result.Error == nil {
			result.Error = &BridgeError{Code: job.State, Message: "Operation requires reconciliation"}
		}
	}
	return result
}

func (c *Client) waitDaemon(ctx context.Context, id string, binding *Binding) (json.RawMessage, error) {
	cursor := int64(0)
	for {
		job, err := c.daemon.job(ctx, id)
		if err != nil {
			return nil, err
		}
		if binding != nil && c.OnProgress != nil {
			c.OnProgress(*binding, operationFromJob(job))
		}
		switch job.State {
		case "succeeded":
			return job.Result, nil
		case "failed", "cancelled", "lost":
			if job.Error != nil {
				return nil, job.Error.bridgeError()
			}
			return nil, &BridgeError{Code: job.State, Message: "Inspect daemon job before retrying"}
		case "waiting_input":
			return nil, &BridgeError{Code: "input_required", Message: "Answer the daemon job prompt explicitly"}
		}
		streamCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		response, err := c.daemon.request(streamCtx, "GET", "/v1/jobs/"+url.PathEscape(id)+"/events", nil, strconv.FormatInt(cursor, 10))
		if err == nil {
			switch response.StatusCode {
			case http.StatusGone:
				cursor = 0
			case http.StatusUnauthorized, http.StatusForbidden:
				_ = response.Body.Close()
				cancel()
				return nil, &BridgeError{Code: "permission_denied", Message: "Daemon job access was revoked"}
			case http.StatusOK:
				scanner := bufio.NewScanner(response.Body)
				scanner.Buffer(make([]byte, 4096), 256*1024)
				for scanner.Scan() {
					line := scanner.Text()
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					var event struct {
						ID       int64  `json:"id"`
						Type     string `json:"type"`
						Stage    string `json:"stage"`
						Message  string `json:"message"`
						Workload string `json:"workload_id"`
					}
					if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
						break
					}
					if event.ID > cursor {
						cursor = event.ID
					}
					if event.Type == "gap" {
						cursor = 0
						break
					}
					if binding != nil && c.OnProgress != nil && (event.Stage != "" || event.Message != "") {
						phase := event.Stage
						if phase == "" {
							phase = event.Message
						}
						c.OnProgress(*binding, Operation{ID: id, State: "running", Phase: phase, WorkloadID: event.Workload})
					}
				}
			}
			_ = response.Body.Close()
		}
		cancel()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (c *Client) ensureDaemon(ctx context.Context, binding Binding, timeout time.Duration) (EnsureResult, error) {
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, job, path, err := c.submitDaemon(ctx, "workloads.ensure_ready", daemonBindingArguments(binding, min(timeout.Seconds(), 3600)), true)
	if err != nil {
		return EnsureResult{}, err
	}
	raw, err := c.waitDaemon(ctx, job, &binding)
	c.daemon.finishActions(ctx, job)
	if err != nil {
		return EnsureResult{}, err
	}
	_ = path
	result, err := decodeDaemonReady(raw, binding)
	if err != nil {
		return EnsureResult{}, err
	}
	// Model cards are advisory SparkRoute policy, separate from host readiness.
	var metadata struct {
		Models map[string]ModelMetadata `json:"model_metadata"`
	}
	metadataCtx, metadataCancel := context.WithTimeout(ctx, 2*time.Second)
	defer metadataCancel()
	_, metadataErr := c.daemon.json(metadataCtx, "POST", "/v1/ops/plugins.sparkroute.endpoints.metadata", map[string]any{
		"workload_id": result.Endpoint.WorkloadID, "recipe_revision": result.Endpoint.RecipeRevision,
		"served_models": result.Endpoint.ServedModels,
	}, "", &metadata)
	if metadataErr == nil {
		for _, model := range result.Endpoint.ServedModels {
			card, exists := metadata.Models[model]
			if !exists {
				continue
			}
			// The live context limit takes precedence over saved recipe metadata.
			if live := result.Endpoint.ModelMetadata[model].Context; live != nil {
				card.Context = live
			}
			result.Endpoint.ModelMetadata[model] = card
		}
	}
	return result, nil
}

func (c *Client) invokeDaemon(ctx context.Context, request bridgeRequest, result any) error {
	if request.Operation == "capabilities" {
		value, err := c.daemonCapabilities(ctx)
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(value)
		return json.Unmarshal(raw, result)
	}
	if request.Operation == "operation_status" {
		if _, err := c.validateDaemon(ctx, nil); err != nil {
			return err
		}
		id, _ := request.Arguments["operation_id"].(string)
		job, err := c.daemon.job(ctx, id)
		if err != nil {
			return err
		}
		c.daemon.finishActions(ctx, id)
		raw, _ := json.Marshal(operationFromJob(job))
		return decodeStrict(raw, result)
	}
	mapping := map[string]string{
		"discover": "plugins.sparkroute.endpoints.discover", "status": "plugins.sparkroute.endpoints.status",
		"resolve": "catalog.recipe", "stop": "workloads.stop",
		"workloads": "workloads.inventory", "sleep": "workloads.sleep",
		"wake": "workloads.wake", "workload_status": "workloads.status",
		"catalog_registries": "catalog.registries", "catalog_clusters": "catalog.clusters", "catalog_search": "catalog.recipes",
		"catalog_resolve": "catalog.recipe", "catalog_import": "catalog.import", "catalog_retain": "catalog.retain",
		"catalog_refresh": "catalog.refresh", "catalog_capacity": "catalog.capacity",
		"catalog_plugins": "workloads.plugins",
	}
	name := mapping[request.Operation]
	if name == "" && request.Operation != "catalog_registry" {
		return fmt.Errorf("unsupported daemon integration operation")
	}
	args := map[string]any{}
	if request.Binding != nil {
		switch request.Operation {
		case "discover", "status":
			args["binding"] = request.Binding
		case "resolve":
			args["reference"], args["expected_revision"], args["overrides"] = request.Binding.Recipe, request.Binding.RecipeRevision, request.Binding.Overrides
		default:
			args = daemonBindingArguments(*request.Binding, 0)
		}
	}
	if request.WorkloadID != "" {
		args["workload_id"] = request.WorkloadID
	}
	for key, value := range request.Arguments {
		args[key] = value
	}
	if request.Operation == "catalog_registry" {
		var err error
		name, args, err = daemonRegistryAction(args)
		if err != nil {
			return err
		}
	}
	mutation := request.Operation == "stop" || request.Operation == "sleep" || request.Operation == "wake" ||
		request.Operation == "catalog_import" || request.Operation == "catalog_retain" || request.Operation == "catalog_refresh" || request.Operation == "catalog_registry"
	raw, job, path, err := c.submitDaemon(ctx, name, args, mutation)
	if err != nil {
		return err
	}
	if job != "" {
		if request.Operation == "catalog_refresh" || request.Operation == "catalog_capacity" {
			value, err := c.daemon.job(ctx, job)
			if err != nil {
				return err
			}
			raw, _ = json.Marshal(operationFromJob(value))
		} else {
			raw, err = c.waitDaemon(ctx, job, nil)
			c.daemon.finishActions(ctx, job)
			if err != nil {
				return err
			}
			_ = path
		}
	}
	raw, err = c.adaptDaemonWorkloads(ctx, request.Operation, raw)
	if err != nil {
		return err
	}
	raw, err = adaptDaemonCatalog(request.Operation, raw)
	if err != nil {
		return err
	}
	if err = decodeStrict(raw, result); err != nil {
		return err
	}
	if discovered, ok := result.(*DiscoverResult); ok {
		for _, endpoint := range discovered.Endpoints {
			if endpoint.ObservedAt <= 0 {
				return fmt.Errorf("daemon endpoint lacks an observation timestamp")
			}
		}
	}
	return nil
}

func adaptDaemonCatalog(operation string, raw json.RawMessage) (json.RawMessage, error) {
	switch operation {
	case "catalog_registries", "catalog_clusters":
		key := "registries"
		if operation == "catalog_clusters" {
			key = "clusters"
		}
		return json.Marshal(map[string]json.RawMessage{key: raw})
	case "catalog_retain":
		return json.RawMessage(`{"retained":true}`), nil
	case "catalog_plugins":
		var value map[string]json.RawMessage
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]json.RawMessage{"plugins": value["plugins"]})
	case "catalog_resolve", "catalog_import":
		return projectCoreRecipe(raw)
	}
	return raw, nil
}

// projectCoreRecipe interprets the registered SparkRoute recipe item. Generic
// catalog selection/trust/revision remain owned by the host's catalog API.
func projectCoreRecipe(raw json.RawMessage) (json.RawMessage, error) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	var items map[string]json.RawMessage
	if err := json.Unmarshal(value["plugin_items"], &items); err != nil {
		return nil, err
	}
	var settings RecipeSettings
	if item := items["sparkroute"]; len(item) != 0 {
		if err := decodeStrict(item, &settings); err != nil {
			return nil, err
		}
	}
	value["sparkroute"], _ = json.Marshal(settings)
	delete(value, "plugin_items")
	var source string
	_ = json.Unmarshal(value["source_kind"], &source)
	kind := "recipe"
	if source == "imported" {
		kind = "snapshot"
	}
	value["profile_source_kind"], _ = json.Marshal(kind)
	delete(value, "source_kind")
	var defaults, overrides, metadata map[string]any
	_ = json.Unmarshal(value["defaults"], &defaults)
	_ = json.Unmarshal(value["normalized_overrides"], &overrides)
	_ = json.Unmarshal(value["metadata"], &metadata)
	delete(value, "normalized_overrides")
	public := map[string]any{"input_price": 0, "output_price": 0}
	if size, ok := metadata["parameters_b"].(float64); ok && size > 0 {
		public["size_b"] = size
	}
	contextValue := defaults["max_model_len"]
	if override, exists := overrides["max_model_len"]; exists {
		contextValue = override
	}
	if length, err := strconv.Atoi(fmt.Sprint(contextValue)); err == nil && length > 0 {
		public["context"] = length
	}
	var runtime string
	_ = json.Unmarshal(value["runtime"], &runtime)
	tags := []string{"local"}
	for _, tag := range []string{runtime, fmt.Sprint(metadata["quantization"])} {
		tag = strings.TrimSpace(strings.ToLower(tag))
		if tag != "" && tag != "<nil>" && len(tag) <= 128 && strings.IndexFunc(tag, func(r rune) bool { return r < 0x20 || r == 0x7f }) < 0 {
			tags = append(tags, tag)
		}
	}
	public["tags"] = tags
	value["model_metadata"], _ = json.Marshal(public)
	return json.Marshal(value)
}

// Updating the optional job ID cannot lose the persisted pre-submission key.
func updateDaemonAction(path string, action daemonAction) error {
	unlock, err := lockDaemonActions(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer unlock()
	raw, err := privateRead(path)
	if err != nil {
		return err
	}
	var old daemonAction
	if err = json.Unmarshal(raw, &old); err != nil {
		return err
	}
	if old.Key != action.Key {
		return fmt.Errorf("daemon action changed while submitting")
	}
	raw, _ = json.Marshal(action)
	file, err := os.CreateTemp(filepath.Dir(path), ".action-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}

func (d *daemonTransport) finishActions(ctx context.Context, id string) {
	if ctx.Err() != nil {
		return
	} // Uncertain/disconnected callers retain intent.
	job, err := d.job(ctx, id)
	if err != nil || (job.State != "succeeded" && job.State != "failed" && job.State != "cancelled") {
		return
	}
	config, err := d.configuration()
	if err != nil {
		return
	}
	unlock, err := lockDaemonActions(config.ActionDirectory)
	if err != nil {
		return
	}
	defer unlock()
	entries, err := os.ReadDir(config.ActionDirectory)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(config.ActionDirectory, entry.Name())
		raw, err := privateRead(path)
		if err != nil {
			continue
		}
		var action daemonAction
		if json.Unmarshal(raw, &action) == nil && action.Job == id {
			_ = os.Remove(path)
		}
	}
}

// The bridge envelope keeps its action field; daemon mutations have named routes.
func daemonRegistryAction(arguments map[string]any) (string, map[string]any, error) {
	action, _ := arguments["action"].(string)
	names := map[string]string{
		"add": "catalog.registries.add", "remove": "catalog.registries.remove",
		"enable": "catalog.registries.enable", "disable": "catalog.registries.disable",
		"trust": "catalog.registries.trust", "untrust": "catalog.registries.untrust",
	}
	name := names[action]
	if name == "" {
		return "", nil, fmt.Errorf("unsupported registry action")
	}
	args := map[string]any{"name": arguments["name"]}
	if action == "add" {
		args["url"] = arguments["url"]
		if subpath, ok := arguments["subpath"]; ok {
			args["subpath"] = subpath
		}
	}
	if action == "trust" {
		args["acknowledge_trust"] = arguments["acknowledge_trust"]
	}
	return name, args, nil
}
