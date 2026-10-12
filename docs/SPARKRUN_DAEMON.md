# Direct Sparkrun daemon client

SparkRoute can use daemon protocol 1 instead of the stdio bridge protocol 5.
Supply `-sparkrun-daemon-connection /private/path/connection.json` (environment:
`SPARKROUTE_SPARKRUN_DAEMON_CONNECTION`). Lifecycle, admin catalog and request-profile
refresh share this selection and never spawn the bridge in daemon mode.

A manually provisioned TCP descriptor has this shape:

```json
{
  "protocol": 1,
  "binding": "CONTROLLER_BINDING_FROM_V1_INFO",
  "profile": "sparkrun",
  "manager": "sparkroute",
  "url": "https://controller.example:8376",
  "token_file": "/private/path/token",
  "ca_file": "/path/to/controller-ca.pem",
  "action_directory": "/private/path/daemon-actions"
}
```

Use `socket` instead of `url` for a same-account Unix socket; retain the manager
token. Locally grant it with `sparkrun daemon token create gateway --scope admin
--manager sparkroute`. The client checks `/v1/info.manager` before every operation;
a default local admin credential cannot impersonate the SparkRoute manager. Omit `ca_file`
when the server uses the system trust store. `token` is accepted directly, but a
private `token_file` supports rotation. Descriptor and token files must be private,
regular files owned by the executing account (Windows validates owner/ACL).
Redirects and ambient HTTP proxies are disabled. Use the daemon's explicit
`--allow-insecure` policy only when intentionally exposing plaintext beyond
loopback; the client does not start or reconfigure a remote daemon.

The managed Sparkrun plugin normally writes a local descriptor with `state_file`
and a dedicated rotating `token_file`. The state file must agree on profile/binding;
restarting the local daemon can change its address without changing the manager.
Credential rotation preserves stable principal `manager:sparkroute` for retained
jobs/retries. A changed controller/profile/manager is rejected. An explicit token
is honored on UDS as well as TCP. Every operation validates protocol and the advertised operation version,
including clients created only for catalog/profile work.

Job submissions return acceptance, then the client follows durable job state and
SSE progress. Per-intent keys and accepted job IDs are persisted under the private
action directory before uncertain network transitions. Different client instances
serialize access to the same intent. A dropped connection preserves the intent;
a new intentional action after known terminal completion receives a new key.
SSE gaps cause state resynchronization. Client timeout/disconnect does not cancel
the daemon job. Lost jobs or expired retry history require explicit recovery.

Core `catalog.*` operations serve catalog/profile data; core `workloads.*` owns
activation, inventory, teardown and provider controls. The Go transport adapts
those common DTOs to gateway types. Plugin operations are limited to endpoint
model-card projections, including optional saved-metadata enrichment after
activation. Endpoint admission still belongs to Go. Adoption preserves the
workload’s original owner; the gateway cannot automatically stop/sleep a workload
owned by another manager.
Discovery and activation responses carry the original `observed_at`: network
retries and cached transport responses cannot extend endpoint freshness. Stale or
implausibly future observations are rejected.

The host/plugin/gateway must be released together using updated compatibility
pins. See the plugin's `docs/SPARKROUTE_DAEMON.md` for opt-in startup, cutover,
ownership and reconciliation; the host's `docs/DAEMON_API.md` is the complete
operation and HTTP route inventory. Shared probe caching is not implemented.
