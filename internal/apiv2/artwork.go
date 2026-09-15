package apiv2

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Silo-Server/silo-server/internal/artworkkey"
	"github.com/Silo-Server/silo-server/internal/artworkstore"
	"github.com/Silo-Server/silo-server/internal/artworkurl"
)

const (
	artworkParamQuery   = "query"
	artworkRangeHeader  = "Range"
	artworkIntegerType  = "integer"
	artworkBinaryFormat = "binary"
)

type ArtworkRepairService interface {
	EnqueueArtworkRepair(context.Context, []string, int) (int, error)
}

// NewArtworkHandler shares the signed asset protocol with secondary listeners.
// It serves only artwork bytes; it does not mount native business operations.
func NewArtworkHandler(store artworkstore.Store, signer *artworkurl.Signer, repair ArtworkRepairService) http.Handler {
	reg := &Registry{deps: Dependencies{ArtworkStore: store, ArtworkSigner: signer, ArtworkRepair: repair}}
	return http.HandlerFunc(reg.serveArtwork)
}

func registerArtwork(reg *Registry) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		id := "getArtwork"
		if method == http.MethodHead {
			id = "headArtwork"
		}
		op := Operation{Operation: humaOp(method, Prefix+"/artwork/{key}", id, "artwork", "Read cached artwork."), Class: ClassPublic, ServiceBacked: true}
		op.Parameters = []*huma.Param{
			{Name: fieldKey, In: paramInPath, Required: true, Description: "Logical artwork key including its nested path.", Schema: &huma.Schema{Type: huma.TypeString}},
			{Name: "exp", In: artworkParamQuery, Required: true, Schema: &huma.Schema{Type: artworkIntegerType, Format: "int64"}},
			{Name: "sig", In: artworkParamQuery, Required: true, Schema: &huma.Schema{Type: huma.TypeString}},
			{Name: ifNoneMatchField, In: paramInHeader, Schema: &huma.Schema{Type: huma.TypeString}},
			{Name: artworkRangeHeader, In: paramInHeader, Schema: &huma.Schema{Type: huma.TypeString}},
		}
		responses := map[string]*huma.Response{"200": {Description: "Artwork bytes"}, "206": {Description: "Partial artwork bytes"}, "304": {Description: "Artwork not modified"}, "404": {Description: "Artwork not found"}, "503": {Description: "Artwork storage unavailable"}}
		if method == http.MethodGet {
			responses["200"].Content = map[string]*huma.MediaType{"image/*": {Schema: &huma.Schema{Type: huma.TypeString, Format: artworkBinaryFormat}}}
			responses["206"].Content = responses["200"].Content
		}
		op.Responses = responses
		RegisterRaw(reg, RawOperation{Operation: op, WildcardParam: fieldKey, Protocol: "artwork-image", Reason: "Artwork bytes and range semantics bypass JSON encoding."}, http.HandlerFunc(reg.serveArtwork))
	}
}

func (reg *Registry) serveArtwork(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, Prefix+"/artwork/")
	if reg.deps.ArtworkStore == nil || reg.deps.ArtworkSigner == nil {
		writeProblem(w, r, NewProblem(TypeNotFound, "Artwork not found."))
		return
	}
	if err := artworkstore.ValidateKey(key); err != nil {
		writeProblem(w, r, NewProblem(TypeNotFound, "Artwork not found."))
		return
	}
	exp, err := strconv.ParseInt(r.URL.Query().Get("exp"), 10, 64)
	if err != nil {
		writeProblem(w, r, NewProblem(TypeNotFound, "Artwork not found."))
		return
	}
	if err := reg.deps.ArtworkSigner.Verify(key, exp, r.URL.Query().Get("sig"), time.Now()); err != nil {
		writeProblem(w, r, NewProblem(TypeNotFound, "Artwork not found."))
		return
	}
	reader, info, err := reg.deps.ArtworkStore.Get(r.Context(), key)
	if err != nil {
		if errors.Is(err, artworkstore.ErrNotFound) {
			if artworkkey.Revision(key) != "" && reg.deps.ArtworkRepair != nil {
				_, _ = reg.deps.ArtworkRepair.EnqueueArtworkRepair(r.Context(), []string{originalRepairKey(key)}, 1)
			}
			writeProblem(w, r, NewProblem(TypeNotFound, "Artwork not found."))
			return
		}
		writeProblem(w, r, unavailable("artwork storage"))
		return
	}
	defer func() { _ = reader.Close() }()

	w.Header().Set("Content-Type", artworkstore.MediaType(key))
	if info.ETag != "" {
		w.Header().Set("ETag", info.ETag)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	seconds := exp - time.Now().Unix()
	if seconds < 0 {
		seconds = 0
	}
	cache := "private, max-age=" + strconv.FormatInt(seconds, 10)
	if artworkkey.Revision(key) != "" {
		cache += ", immutable"
	}
	w.Header().Set("Cache-Control", cache)
	if r.Header.Get(ifNoneMatchField) != "" && r.Header.Get(ifNoneMatchField) == info.ETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if content, ok := reader.(io.ReadSeeker); ok {
		http.ServeContent(w, r, path.Base(key), info.ModTime, content)
		return
	}
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, reader)
	}
}

func originalRepairKey(key string) string {
	dir := artworkkey.Directory(key)
	if dir == "" {
		return key
	}
	base := path.Base(key)
	ext := path.Ext(base)
	stem := base[:len(base)-len(ext)]
	dot := strings.IndexByte(stem, '.')
	if dot < 0 {
		return key
	}
	return dir + "original" + stem[dot:] + ext
}
