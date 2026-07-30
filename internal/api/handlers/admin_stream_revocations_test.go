package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
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

func TestStreamRevocationCapabilitiesAdvertisesKillListSurface(t *testing.T) {
	h := NewAdminStreamRevocationHandler(streamrevoke.New(streamrevoke.Options{}))
	rr := httptest.NewRecorder()
	h.HandleGetCapabilities(rr, httptest.NewRequest(http.MethodGet, "/revocations/capabilities", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got streamRevocationCapabilitiesResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if !got.StreamRevocations || !got.StreamRevocationUnrevoke {
		t.Fatalf("capabilities did not advertise the kill list: %+v", got)
	}
	if len(got.StreamRevocationKinds) == 0 {
		t.Fatal("no revocation kinds advertised")
	}
}

// The advertised {kind} vocabulary must be exactly what the wire parser accepts,
// in both directions. Sampling a handful of rejected strings is not enough: it
// cannot catch a kind the parser starts accepting without advertising it. Since
// both now read streamRevocationKindsByWire, the check is over the whole map.
func TestAdvertisedRevocationKindsMatchWireParser(t *testing.T) {
	advertised := streamRevocationKinds()
	if len(advertised) != len(streamRevocationKindsByWire) {
		t.Fatalf("advertised %d kinds but the parser accepts %d; the two have drifted",
			len(advertised), len(streamRevocationKindsByWire))
	}
	for _, kind := range advertised {
		if _, ok := streamRevocationKindsByWire[kind]; !ok {
			t.Errorf("advertised kind %q is not in the parser vocabulary", kind)
		}
		if _, err := revocationKeyFromWire(kind, "1"); err != nil {
			t.Errorf("advertised kind %q rejected by revocationKeyFromWire: %v", kind, err)
		}
	}
	for kind := range streamRevocationKindsByWire {
		if !slices.Contains(advertised, kind) {
			t.Errorf("parser accepts kind %q but it is not advertised", kind)
		}
	}
	for _, kind := range []string{"", "users", "sessions", "profile", "USER", "device"} {
		if _, err := revocationKeyFromWire(kind, "1"); err == nil {
			t.Errorf("kind %q outside the vocabulary was accepted", kind)
		}
	}
}

func TestAdvertisedRevocationKindsAreSortedAndStable(t *testing.T) {
	first := streamRevocationKinds()
	if !slices.IsSorted(first) {
		t.Errorf("advertised kinds are not sorted: %v", first)
	}
	// Map iteration order is randomised, so an unsorted implementation would
	// return a different order across calls for the same wire contract.
	for i := 0; i < 20; i++ {
		if got := streamRevocationKinds(); !slices.Equal(got, first) {
			t.Fatalf("advertised kinds unstable across calls: %v then %v", first, got)
		}
	}
}

func TestCapabilitiesKindsAreNotAliasedToPackageState(t *testing.T) {
	h := NewAdminStreamRevocationHandler(streamrevoke.New(streamrevoke.Options{}))
	rr := httptest.NewRecorder()
	h.HandleGetCapabilities(rr, httptest.NewRequest(http.MethodGet, "/revocations/capabilities", nil))
	var got streamRevocationCapabilitiesResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	got.StreamRevocationKinds[0] = "mutated"
	if slices.Contains(streamRevocationKinds(), "mutated") {
		t.Fatal("handler returned a slice aliasing package state; a caller can corrupt the vocabulary")
	}
}
