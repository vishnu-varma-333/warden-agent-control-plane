package httpapi

import (
	"net/http"

	"github.com/vishnu-varma-333/warden-agent-control-plane/internal/budget"
)

// SpendHandler exposes current budget consumption to the console's spend
// view. Read-only: it never charges anything.
type SpendHandler struct {
	Budget *budget.Budget
}

func (h *SpendHandler) List(w http.ResponseWriter, r *http.Request) {
	scopes, err := h.Budget.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, scopes)
}
