package apiv2

import (
	"context"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

// AdminJobArtifactService opens a completed job's artifact. It exists so a
// store that cannot presign still has a way to deliver bytes.
type AdminJobArtifactService interface {
	OpenAdminJobArtifact(context.Context, string) (handlers.AdminJobArtifactDownload, error)
}

// registerAdminJobArtifactDownload serves an artifact to a signed capability
// rather than a session. The presigned S3 URL this replaces authorized itself,
// and the web UI opens the URL in a new tab with no Authorization header, so an
// administrator-gated route would answer 401. The signature is scoped to one
// job ID under its own capability domain, so an artwork URL cannot be replayed
// here and a URL for one job does not read another's artifact.
func registerAdminJobArtifactDownload(reg *Registry) {
	operation := humaOp("GET", Prefix+"/admin/jobs/{id}/artifact", "downloadAdminJobArtifact", "admin-tasks",
		"Stream a completed job's artifact through the API host. Authorized by the signed capability in the download URL, not by a session.")
	operation.Parameters = []*huma.Param{
		{Name: "id", In: paramInPath, Required: true, Schema: &huma.Schema{Type: huma.TypeString, MinLength: new(1), MaxLength: new(128)}},
		{Name: "exp", In: artworkParamQuery, Required: true, Schema: &huma.Schema{Type: artworkIntegerType, Format: "int64"}},
		{Name: "sig", In: artworkParamQuery, Required: true, Schema: &huma.Schema{Type: huma.TypeString}},
	}
	operation.Responses = map[string]*huma.Response{
		"200": {
			Description: "Gzip-compressed job artifact",
			Content:     map[string]*huma.MediaType{"application/gzip": {Schema: &huma.Schema{Type: huma.TypeString, Format: artworkBinaryFormat}}},
			Headers: map[string]*huma.Param{
				"Content-Disposition": {Schema: &huma.Schema{Type: huma.TypeString}},
				"Content-Length":      {Schema: &huma.Schema{Type: artworkIntegerType}},
			},
		},
		"404": {Description: "Artifact not found, or the capability is invalid or expired"},
		"503": {Description: "Artifact storage unavailable"},
	}
	RegisterRaw(reg, RawOperation{
		Operation: Operation{Operation: operation, Class: ClassPublic, ServiceBacked: true},
		Protocol:  "job-artifact",
		Reason:    "Artifact bytes must not pass through JSON encoding, and the signed capability replaces the presigned storage URL a session-gated route could not provide.",
	}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		// Every rejection answers 404 so the route never reveals whether a job
		// exists to a caller holding no valid capability.
		if strings.TrimSpace(id) == "" || len(id) > 128 || reg.deps.AdminJobArtifacts == nil || reg.deps.AdminJobArtifactSigner == nil {
			writeProblem(w, r, NewProblem(TypeNotFound, "Job artifact not found."))
			return
		}
		exp, err := strconv.ParseInt(r.URL.Query().Get("exp"), 10, 64)
		if err != nil {
			writeProblem(w, r, NewProblem(TypeNotFound, "Job artifact not found."))
			return
		}
		if err := reg.deps.AdminJobArtifactSigner.Verify(id, exp, r.URL.Query().Get("sig"), time.Now()); err != nil {
			writeProblem(w, r, NewProblem(TypeNotFound, "Job artifact not found."))
			return
		}
		download, err := reg.deps.AdminJobArtifacts.OpenAdminJobArtifact(r.Context(), id)
		if err != nil || download.Body == nil {
			writeProblem(w, r, NewProblem(TypeNotFound, "Job artifact not found."))
			return
		}
		defer func() { _ = download.Body.Close() }()
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": download.Filename}))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Accept-Ranges", "none")
		if download.Size != nil && *download.Size >= 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(*download.Size, 10))
		}
		w.WriteHeader(http.StatusOK)
		if _, err := io.Copy(w, download.Body); err != nil {
			slog.WarnContext(r.Context(), "job artifact stream interrupted", "component", "adminjob", "job_id", id)
		}
	}))
}
