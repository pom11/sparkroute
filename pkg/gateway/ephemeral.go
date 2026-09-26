// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only
package gateway

import (
	"context"
	"net/http"
)

type ephemeralInferenceKey struct{}

// WithoutSavedTraces disables payload recording for trusted in-process callers.
// Authentication, routing, guardrails, privacy, usage and metrics are unchanged.
// An HTTP header cannot opt a normal client out of the configured trace policy.
func WithoutSavedTraces(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ephemeralInferenceKey{}, true)))
	})
}
