package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/streamrevoke"
	"github.com/go-chi/chi/v5"
)

const (
	maxStreamRevocationBodyBytes = 16 << 10
	maxStreamRevocationIDLength  = 256
	maxStreamRevocationReason    = 512
	maxStreamRevocationTTL       = 30 * 24 * 60 * 60
)

// AdminStreamRevocationHandler exposes the operator-visible stream kill list.
// Unrevoking an over-cap victim is legal, but the async enforcer may revoke it
// again on its next pass while the owning user remains over cap.
type AdminStreamRevocationHandler struct {
	store *streamrevoke.Store
}

func NewAdminStreamRevocationHandler(store *streamrevoke.Store) *AdminStreamRevocationHandler {
	return &AdminStreamRevocationHandler{store: store}
}

type streamRevocationRequest struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	Reason     string `json:"reason"`
	TTLSeconds *int64 `json:"ttl_seconds"`
}

type streamRevocationResponse struct {
	streamrevoke.Revocation
	Warnings []string `json:"warnings,omitempty"`
}

// streamRevocationKindsByWire is the single source of truth for the closed
// {kind} vocabulary the revocation routes accept. Both the wire parser
// (revocationKeyFromWire) and the capability advertisement (streamRevocationKinds)
// read it, so adding a kind here automatically advertises it and the two cannot
// drift apart. An earlier version duplicated the list in a switch statement,
// which meant a newly-accepted kind could go unadvertised — clients would then
// feature-detect an incomplete vocabulary.
var streamRevocationKindsByWire = map[string]streamrevoke.Kind{
	"session":                        streamrevoke.KindSession,
	string(streamrevoke.KindSession): streamrevoke.KindSession,
	string(streamrevoke.KindUser):    streamrevoke.KindUser,
}

// streamRevocationKinds returns the accepted {kind} values in sorted order.
// Sorted because map iteration order is randomised and the advertisement is a
// wire response that must be stable across calls.
func streamRevocationKinds() []string {
	kinds := make([]string, 0, len(streamRevocationKindsByWire))
	for kind := range streamRevocationKindsByWire {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

// streamRevocationCapabilitiesResponse advertises the operator kill-list
// endpoints so clients and operator tooling can feature-detect them rather than
// probing for a 404. These routes are mounted only when a revocation store is
// wired, which is why this endpoint is mounted in the same block.
type streamRevocationCapabilitiesResponse struct {
	// StreamRevocations reports that the admin kill-list endpoints exist.
	StreamRevocations bool `json:"stream_revocations"`
	// StreamRevocationKinds is the closed {kind} vocabulary accepted by
	// DELETE /admin/streams/revocations/{kind}/{id}.
	StreamRevocationKinds []string `json:"stream_revocation_kinds"`
	// StreamRevocationUnrevoke reports that revocations can be lifted, not just
	// listed and created.
	StreamRevocationUnrevoke bool `json:"stream_revocation_unrevoke"`
}

// HandleGetCapabilities exposes additive feature support for the stream
// kill list (GET /admin/streams/revocations/capabilities).
func (h *AdminStreamRevocationHandler) HandleGetCapabilities(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, streamRevocationCapabilitiesResponse{
		StreamRevocations:        true,
		StreamRevocationKinds:    streamRevocationKinds(),
		StreamRevocationUnrevoke: true,
	})
}

func (h *AdminStreamRevocationHandler) HandleList(w http.ResponseWriter, _ *http.Request) {
	revocations := h.store.List()
	sort.Slice(revocations, func(i, j int) bool {
		return revocations[i].RevokedAt.After(revocations[j].RevokedAt)
	})
	writeJSON(w, http.StatusOK, map[string]any{"revocations": revocations})
}

func (h *AdminStreamRevocationHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var req streamRevocationRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxStreamRevocationBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}

	key, err := revocationKeyFromWire(req.Kind, req.ID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if len(reason) > maxStreamRevocationReason {
		writeError(w, http.StatusBadRequest, "bad_request", "Reason is too long")
		return
	}

	var until time.Time
	if req.TTLSeconds != nil {
		if *req.TTLSeconds <= 0 || *req.TTLSeconds > maxStreamRevocationTTL {
			writeError(w, http.StatusBadRequest, "bad_request", "ttl_seconds must be between 1 and 2592000")
			return
		}
		until = time.Now().Add(time.Duration(*req.TTLSeconds) * time.Second)
	} else {
		// A zero expiry asks the store to use its default via the public helper.
		var warnings []string
		switch key.Kind {
		case streamrevoke.KindSession:
			warnings, err = h.store.RevokeSessionWithWarnings(r.Context(), key.ID, reason)
		case streamrevoke.KindUser:
			userID, _ := strconv.Atoi(key.ID)
			warnings, err = h.store.RevokeUserWithWarnings(r.Context(), userID, reason)
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create revocation")
			return
		}
		writeJSON(w, http.StatusCreated, streamRevocationResponse{Revocation: findRevocation(h.store, key), Warnings: warnings})
		return
	}

	warnings, err := h.store.RevokeWithWarnings(r.Context(), key, reason, until)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create revocation")
		return
	}
	writeJSON(w, http.StatusCreated, streamRevocationResponse{Revocation: findRevocation(h.store, key), Warnings: warnings})
}

func (h *AdminStreamRevocationHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	key, err := revocationKeyFromWire(chi.URLParam(r, "kind"), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	warnings, err := h.store.UnrevokeWithWarnings(r.Context(), key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to remove revocation")
		return
	}
	if len(warnings) > 0 {
		writeJSON(w, http.StatusOK, map[string]any{"warnings": warnings})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func revocationKeyFromWire(kind, id string) (streamrevoke.Key, error) {
	internalKind, ok := streamRevocationKindsByWire[kind]
	if !ok {
		return streamrevoke.Key{}, fmt.Errorf("kind must be one of %s", strings.Join(streamRevocationKinds(), ", "))
	}

	if internalKind == streamrevoke.KindSession {
		id = strings.TrimSpace(id)
		if id == "" {
			return streamrevoke.Key{}, errors.New("session id is required")
		}
		if len(id) > maxStreamRevocationIDLength {
			return streamrevoke.Key{}, errors.New("session id is too long")
		}
		return streamrevoke.Key{Kind: internalKind, ID: id}, nil
	}

	userID, err := strconv.Atoi(id)
	if err != nil || userID <= 0 || strconv.Itoa(userID) != id {
		return streamrevoke.Key{}, errors.New("user id must be a canonical positive integer")
	}
	return streamrevoke.Key{Kind: internalKind, ID: id}, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func findRevocation(store *streamrevoke.Store, key streamrevoke.Key) streamrevoke.Revocation {
	for _, revocation := range store.List() {
		if revocation.Kind == key.Kind && revocation.ID == key.ID {
			return revocation
		}
	}
	return streamrevoke.Revocation{Kind: key.Kind, ID: key.ID}
}
