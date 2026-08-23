package handlers

import (
	"context"
	"log/slog"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
)

// Reconstruction replays a recipe that was committed before the H.264
// copy-safety verdict for its source was known. That is safe for everything
// except a video stream-copy: the optimistic-remux race resolves the verdict
// behind the play, and the only mechanism that withdraws a condemned remux —
// CopySafetyNotifier — reaches the in-process sessions of the replica that
// reached the verdict.
//
// A client whose replica dies between the verdict landing and the notification
// retries its signed stream URL elsewhere. The replacement replica has no live
// session, so it rebuilds one from the card and re-serves the same unsafe
// remux, with nothing left to withdraw it. Re-checking the persisted verdict at
// the moment of reconstruction closes that hole: the row is the one piece of
// state every replica shares.
//
// The refusal is a plain not-found, matching an expired or missing recipe,
// because that is the failure a client's recovery already knows how to handle
// — it mints a fresh attempt, which plans against the persisted verdict and
// lands on a transcode.

// videoCopyReconstructRefused reports whether rebuilding a lost transport from
// card must be refused because the persisted verdict now says its source cannot
// be video stream-copied. Only copy deliveries are gated; a transcode
// reconstruct is never touched.
//
// An unreadable row is not evidence of anything and does not refuse: the
// verdict is re-checked on every request, so a database blip costs a later
// refusal rather than a spurious one. The same applies to a handler with no
// file resolver wired (optional on PlaybackHandler).
func videoCopyReconstructRefused(ctx context.Context, files FilePathResolver, card *playback.RecipeCard) bool {
	if card == nil || files == nil || card.MediaFileID <= 0 || !card.VideoStreamCopy() {
		return false
	}
	file, err := files.GetByID(ctx, card.MediaFileID)
	if err != nil || file == nil {
		return false
	}
	return videoCopyUnsafeByVerdict(ctx, file, card.SessionID)
}

// reconstructTransportForServe rebuilds a lost local transport from the token
// recipe for the manifest and segment serve routes, refusing a video
// stream-copy the persisted copy-safety verdict has since condemned. A nil
// result is the caller's not-found, which is also what a missing card yields —
// the two are the same thing to a client: this recipe is no longer serveable.
func (h *PlaybackHandler) reconstructTransportForServe(ctx context.Context, sessionID string, requestedSegment int, card *playback.RecipeCard) *playback.TranscodeSession {
	if card == nil {
		return nil
	}
	if videoCopyReconstructRefused(ctx, h.fileResolver, card) {
		return nil
	}
	return h.tm.ReconstructTranscode(ctx, sessionID, requestedSegment, *card)
}

// videoCopyUnsafeByVerdict reports whether the media_files row carries a valid
// verdict condemning a video stream-copy of this file, logging the refusal it
// is about to cause.
func videoCopyUnsafeByVerdict(ctx context.Context, file *models.MediaFile, sessionID string) bool {
	multi, known := file.PersistedVideoCopyVerdict()
	if !known || !multi {
		return false
	}
	slog.InfoContext(ctx, "refusing to reconstruct a copy-unsafe video stream-copy",
		"component", "api",
		"session", sessionID,
		"playback_session_id", sessionID,
		"file_id", file.ID,
		"reason", playback.PlanInvalidatedVideoCopyUnsafe,
	)
	return true
}
