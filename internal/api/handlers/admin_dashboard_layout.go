package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
)

// maxDashboardLayoutBytes bounds the PUT body. The layout is a short list of
// widget ids and spans; 16 KiB leaves generous headroom while keeping the blob
// small enough that last-write-wins per admin account stays cheap.
const maxDashboardLayoutBytes = 16 << 10

// adminDashboardLayoutResponse is the GET body. Both fields are null when the
// admin has never saved a layout, which the web client reads as "keep the
// local/default layout" rather than as an error.
type adminDashboardLayoutResponse struct {
	Layout    json.RawMessage `json:"layout"`
	UpdatedAt *time.Time      `json:"updated_at"`
}

type adminDashboardLayoutRequest struct {
	Layout json.RawMessage `json:"layout"`
}

// Sentinel validation failures. Their text is the message the client sees, so
// it stays lowercase (staticcheck ST1005) and reads as a sentence fragment.
var (
	errDashboardLayoutInvalidJSON = errors.New("request body must be valid JSON")
	errDashboardLayoutMissing     = errors.New("layout is required")
	errDashboardLayoutNotObject   = errors.New("layout must be a JSON object")
)

// parseDashboardLayoutPayload validates a PUT body and returns the document to
// store. The server treats the layout as opaque past requiring a JSON object:
// widget ids and spans are the web client's vocabulary, and it already
// sanitizes them on load, so validating them here would only add a second
// place to update whenever a widget is added.
func parseDashboardLayoutPayload(body []byte) (json.RawMessage, error) {
	var req adminDashboardLayoutRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, errDashboardLayoutInvalidJSON
	}
	// Unmarshal already checked the syntax of the whole document, so the first
	// non-space byte is enough to tell an object from any other JSON value.
	raw := json.RawMessage(bytes.TrimSpace(req.Layout))
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, errDashboardLayoutMissing
	}
	if raw[0] != '{' {
		return nil, errDashboardLayoutNotObject
	}
	return raw, nil
}

// HandleGetDashboardLayout handles GET /admin/dashboard/layout.
func (h *AdminHandler) HandleGetDashboardLayout(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Database not configured")
		return
	}
	userID := apimw.GetUserID(r.Context())
	if userID == 0 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}

	var (
		layout    json.RawMessage
		updatedAt time.Time
	)
	err := h.pool.QueryRow(r.Context(),
		`SELECT layout, updated_at FROM admin_dashboard_layouts WHERE user_id = $1`,
		userID,
	).Scan(&layout, &updatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeJSON(w, http.StatusOK, adminDashboardLayoutResponse{})
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load dashboard layout")
		return
	}

	writeJSON(w, http.StatusOK, adminDashboardLayoutResponse{Layout: layout, UpdatedAt: &updatedAt})
}

// HandlePutDashboardLayout handles PUT /admin/dashboard/layout.
func (h *AdminHandler) HandlePutDashboardLayout(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Database not configured")
		return
	}
	userID := apimw.GetUserID(r.Context())
	if userID == 0 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDashboardLayoutBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusBadRequest, "bad_request", "Dashboard layout is too large")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}

	layout, err := parseDashboardLayoutPayload(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	// Last write wins. The layout is a per-admin blob, so a race between two of
	// the same admin's tabs can only cost the older arrangement; updated_at is
	// returned by GET so a compare-and-set could be layered on later.
	if _, err := h.pool.Exec(r.Context(),
		`INSERT INTO admin_dashboard_layouts (user_id, layout, updated_at)
		 VALUES ($1, $2, now())
		 ON CONFLICT (user_id) DO UPDATE SET layout = EXCLUDED.layout, updated_at = now()`,
		userID, []byte(layout),
	); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to save dashboard layout")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleDeleteDashboardLayout handles DELETE /admin/dashboard/layout. Deleting
// the row resets the admin to the default layout; it is idempotent.
func (h *AdminHandler) HandleDeleteDashboardLayout(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Database not configured")
		return
	}
	userID := apimw.GetUserID(r.Context())
	if userID == 0 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}

	if _, err := h.pool.Exec(r.Context(),
		`DELETE FROM admin_dashboard_layouts WHERE user_id = $1`, userID,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to reset dashboard layout")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
