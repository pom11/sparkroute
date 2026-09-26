<!--
SPDX-FileCopyrightText: 2026 Scitrera LLC
SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
SPDX-License-Identifier: AGPL-3.0-only
-->

# Ephemeral chat tests

Open **Chat test** in the admin console (`/admin/chat`). Choose a configured virtual model, linked request profile, alias, or model-routing selector. Type in the model field to filter names by a case-insensitive contains match; use the mouse or arrow keys and Enter to select. The panel supports text conversations through Chat Completions and Responses, including normal protocol translation and deployment-specific profile parameters.

The response streams into the page. Expand **Reasoning** when supplied by the runtime. Compare total time, time to first text/reasoning, and reported output tokens. These timings include routing and possible activation; they are spot checks, not controlled benchmark scores. **Stop** cancels the request. **Clear conversation**, navigating away, or reloading clears the conversation. Changing the model/profile, request API, or token limit preserves the transcript and draft; the next request sends the conversation to the newly selected target. Each response retains its original model label and reasoning. The message composer stays at the bottom while the transcript scrolls. No history is stored in browser storage.

Tests run with the admin's `config_write` authorization and preserve operator attribution. They use ordinary routing, guardrails, privacy processing, and usage accounting, including possible on-demand activation. Admin credentials are not forwarded upstream. Reader-only admins cannot run tests. Hidden models are available to operators; internal models remain unavailable as direct targets.

The server accepts only bounded text messages, model/API selection, and an output limit (1–8192 tokens, default 1024). It sends streaming, stateless requests (`store:false`), and disables SparkRoute saved payload traces using a private in-process context flag. Ordinary gateway requests retain their configured trace policy. Usage/metrics records and upstream provider retention policies still apply.

This first panel does not execute tools, upload images/files, or implement durable Responses conversation state. Tool-use quality belongs in the proposed benchmark workflow. Request-profile overrides can change effective generation parameters, including token limits, just as they do for other clients.

Admin API routes: `GET /v1/chat-test/models`, `POST /v1/chat-test`. Both require `config_write`; they are enabled only when the admin service is wired to the running data plane.
