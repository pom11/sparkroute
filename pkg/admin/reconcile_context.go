// SPDX-FileCopyrightText: 2026 Scitrera LLC
// SPDX-FileCopyrightText: 2026 Fox Engine Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package admin

import (
	"net/http"

	"github.com/sparksq/sparkroute/pkg/config/managed"
)

func (h *handler) reconcileContext(writer http.ResponseWriter, request *http.Request, owner managed.Owner) {
	if owner != managed.OwnerSparkrun {
		writeError(writer, http.StatusNotFound, "not_found", "route not found")
		return
	}
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	if !authorizeManagedSetRead(writer, request, owner) {
		return
	}
	store, ok := h.options.ManagedConfig.(managed.ReconcileContextStore)
	if !ok {
		writeError(writer, http.StatusNotImplemented, "not_supported", "reconciliation context is unavailable")
		return
	}
	result, err := store.ReconcileContext(request.Context(), owner)
	if err != nil {
		writeManagedConfigError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, result)
}
