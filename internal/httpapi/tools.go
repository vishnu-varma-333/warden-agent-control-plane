package httpapi

import (
	"net/http"

	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/registry"
)

// ToolsHandler exposes the registry to the console's tools view: what
// Warden has pinned from every upstream MCP server, and the one admin
// action available on it — approving a changed definition.
type ToolsHandler struct {
	Registry *registry.Registry
}

func (h *ToolsHandler) List(w http.ResponseWriter, r *http.Request) {
	tools, err := h.Registry.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, tools)
}

func (h *ToolsHandler) Approve(w http.ResponseWriter, r *http.Request) {
	server := r.PathValue("server")
	name := r.PathValue("name")
	if err := h.Registry.Approve(r.Context(), server, name); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
