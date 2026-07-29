package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/streamrevoke"
	"github.com/go-chi/chi/v5"
)

func streamRevocationTestRouter(store *streamrevoke.Store) http.Handler {
	h := NewAdminStreamRevocationHandler(store)
	r := chi.NewRouter()
	r.Get("/revocations", h.HandleList)
	r.Post("/revocations", h.HandleCreate)
	r.Delete("/revocations/{kind}/{id}", h.HandleDelete)
	return r
}

func TestAdminStreamRevocationRequiresAdminMiddleware(t *testing.T) {
	const adminRole = "admin"
	protected := apimw.RequireActingAdmin(nil)(streamRevocationTestRouter(streamrevoke.New(streamrevoke.Options{})))
	for _, tc := range []struct {
		name   string
		claims *auth.Claims
		status int
	}{
		{name: "unauthenticated", status: http.StatusUnauthorized},
		{name: "non admin", claims: &auth.Claims{UserID: 1, Role: string(scopeUser)}, status: http.StatusForbidden},
		{name: adminRole, claims: &auth.Claims{UserID: 1, Role: adminRole}, status: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/revocations", nil)
			if tc.claims != nil {
				req = req.WithContext(apimw.SetClaims(req.Context(), tc.claims))
			}
			rec := httptest.NewRecorder()
			protected.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status=%d, want %d", rec.Code, tc.status)
			}
		})
	}
}

func TestAdminStreamRevocationSessionMappingAndListDeleteRoundTrip(t *testing.T) {
	store := streamrevoke.New(streamrevoke.Options{})
	router := streamRevocationTestRouter(store)
	req := httptest.NewRequest(http.MethodPost, "/revocations", bytes.NewBufferString(`{"kind":"session","id":" s1 ","reason":" test "}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created["kind"] != "sess" || created["id"] != "s1" {
		t.Fatalf("created=%v, want internal sess kind and trimmed id", created)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/revocations", nil)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	var listed struct {
		Revocations []streamrevoke.Revocation `json:"revocations"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Revocations) != 1 {
		t.Fatalf("list=%+v", listed.Revocations)
	}
	deletePath := "/revocations/" + string(listed.Revocations[0].Kind) + "/" + listed.Revocations[0].ID
	deleteReq := httptest.NewRequest(http.MethodDelete, deletePath, nil)
	deleteRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent || len(store.List()) != 0 {
		t.Fatalf("delete status=%d remaining=%v", deleteRec.Code, store.List())
	}
}

func TestAdminStreamRevocationValidation(t *testing.T) {
	router := streamRevocationTestRouter(streamrevoke.New(streamrevoke.Options{}))
	tests := []struct {
		name string
		body string
	}{
		{name: "noncanonical user", body: `{"kind":"user","id":"01"}`},
		{name: "zero user", body: `{"kind":"user","id":"0"}`},
		{name: "unknown kind", body: `{"kind":"stream","id":"x"}`},
		{name: "missing session id", body: `{"kind":"session","id":" "}`},
		{name: "zero ttl", body: `{"kind":"sess","id":"x","ttl_seconds":0}`},
		{name: "negative ttl", body: `{"kind":"sess","id":"x","ttl_seconds":-1}`},
		{name: "ttl above max", body: `{"kind":"sess","id":"x","ttl_seconds":2592001}`},
		{name: "ttl integer overflow", body: `{"kind":"sess","id":"x","ttl_seconds":9223372036854775808}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/revocations", bytes.NewBufferString(tt.body))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAdminStreamRevocationDeleteAbsentIsIdempotent(t *testing.T) {
	router := streamRevocationTestRouter(streamrevoke.New(streamrevoke.Options{}))
	req := httptest.NewRequest(http.MethodDelete, "/revocations/sess/missing", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
