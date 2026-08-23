package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
)

// copySafetyStreamFile builds an H.264 file on disk with an optional persisted
// multi-PPS verdict, valid for the size and mtime the row reports.
func copySafetyStreamFile(t *testing.T, multiplePPS *bool) *models.MediaFile {
	t.Helper()
	mtime := time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)
	modified := mtime
	file := &models.MediaFile{
		ID:             42,
		ContentID:      "movie-1",
		FilePath:       writePlaybackTestMediaFile(t, "movie.mkv"),
		FileSize:       1234,
		FileModifiedAt: &modified,
		CodecVideo:     "h264",
		CodecAudio:     "aac",
		VideoTracks:    []models.VideoTrack{{Codec: "h264"}},
		Duration:       3600,
	}
	if multiplePPS != nil {
		verdict := *multiplePPS
		scanSize := file.FileSize
		scanMtime := mtime
		file.MultiplePPS = &verdict
		file.MultiplePPSScanSize = &scanSize
		file.MultiplePPSScanMtime = &scanMtime
	}
	return file
}

// A signed stream URL is a durable capability: the client replays it on
// whichever replica answers next. When the replica that started the play dies
// between the copy-safety scan persisting a multi-PPS verdict and the in-process
// notification going out, the retry lands somewhere with no live session, and
// the recipe card rebuilds the very remux the verdict condemned — with no
// notifier left anywhere that could withdraw it. The reconstruct has to consult
// the row, which is the only state the replicas share.
func TestHandleStream_RefusesReconstructingACopyUnsafeRemux(t *testing.T) {
	const (
		secret    = "test-stream-signing-secret"
		sessionID = "lost-remux-session"
	)
	unsafe := true
	file := copySafetyStreamFile(t, &unsafe)

	sessionMgr := playback.NewSessionManager(0, 0)
	tm := playback.NewTranscodeManager()
	tm.Sessions = sessionMgr

	handler := NewStreamHandler(sessionMgr, testPlaybackFileResolver{file: file})
	handler.TM = tm
	handler.JWTSecret = secret

	card := playback.NewRemuxRecipeCard(sessionID, 1, "profile-1", file.ID, false, 0)
	card.InputPath = file.FilePath
	token, err := streamtoken.Sign(card.ToClaims(), secret, playback.MaxTokenTTL)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stream/"+sessionID+"?st="+token, nil)
	req = req.WithContext(newAuthorizedPlaybackContext())
	req = withPlaybackRouteParam(req, "session_id", sessionID)

	rr := httptest.NewRecorder()
	handler.HandleStream(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s; want the reconstruct refused as not-found", rr.Code, rr.Body.String())
	}
	// The refusal must not leave the session it rebuilt behind: the client's
	// recovery mints a fresh attempt, and this one has no route left.
	if _, err := sessionMgr.GetSession(sessionID); !errors.Is(err, playback.ErrSessionNotFound) {
		t.Fatalf("GetSession error = %v, want the refused reconstruction torn down", err)
	}
}

// The gate is the verdict, not the route: a remux whose source has no verdict,
// or a verdict saying the copy is safe, still reconstructs and streams.
func TestHandleStream_ReconstructsARemuxThatIsStillCopySafe(t *testing.T) {
	const secret = "test-stream-signing-secret"
	safe := false

	for _, tc := range []struct {
		name      string
		sessionID string
		verdict   *bool
	}{
		{name: "verdict says safe", sessionID: "lost-remux-safe", verdict: &safe},
		{name: "verdict unknown", sessionID: "lost-remux-unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sessionID := tc.sessionID
			file := copySafetyStreamFile(t, tc.verdict)

			sessionMgr := playback.NewSessionManager(0, 0)
			tm := playback.NewTranscodeManager()
			tm.Sessions = sessionMgr

			ffmpeg := filepath.Join(t.TempDir(), "ffmpeg")
			if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\nprintf muxed\n"), 0o755); err != nil {
				t.Fatalf("write fake ffmpeg: %v", err)
			}
			handler := NewStreamHandler(sessionMgr, testPlaybackFileResolver{file: file})
			handler.TM = tm
			handler.JWTSecret = secret
			handler.PlaybackConfig = func() config.PlaybackConfig {
				return config.PlaybackConfig{FFmpegPath: ffmpeg}
			}

			card := playback.NewRemuxRecipeCard(sessionID, 1, "profile-1", file.ID, false, 0)
			card.InputPath = file.FilePath
			token, err := streamtoken.Sign(card.ToClaims(), secret, playback.MaxTokenTTL)
			if err != nil {
				t.Fatalf("Sign: %v", err)
			}

			req := httptest.NewRequest(http.MethodGet, "/api/v1/stream/"+sessionID+"?st="+token, nil)
			req = req.WithContext(newAuthorizedPlaybackContext())
			req = withPlaybackRouteParam(req, "session_id", sessionID)

			rr := httptest.NewRecorder()
			handler.HandleStream(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s; want the reconstruct served", rr.Code, rr.Body.String())
			}
		})
	}
}

// Only video stream-copy deliveries are gated. A transcode re-encodes the
// bitstream, so conflicting parameter sets cannot reach the client's decoder
// and the recipe stays serveable whatever the verdict says.
func TestVideoCopyReconstructRefusedOnlyGatesCopyDeliveries(t *testing.T) {
	unsafe := true
	file := copySafetyStreamFile(t, &unsafe)
	files := testPlaybackFileResolver{file: file}

	remux := playback.NewRemuxRecipeCard("s", 1, "profile-1", file.ID, false, 0)
	copyHLS := playback.RecipeCard{SessionID: "s", UserID: 1, MediaFileID: file.ID, PlayMethod: playback.PlayTranscode, TargetCodecVideo: "copy"}
	transcode := playback.RecipeCard{SessionID: "s", UserID: 1, MediaFileID: file.ID, PlayMethod: playback.PlayTranscode, TargetCodecVideo: "h264"}
	direct := playback.NewDirectRecipeCard("s", 1, "profile-1", file.ID)

	for _, tc := range []struct {
		name string
		card playback.RecipeCard
		want bool
	}{
		{name: "progressive remux", card: remux, want: true},
		{name: "hls video copy", card: copyHLS, want: true},
		{name: "real transcode", card: transcode},
		{name: "direct play", card: direct},
	} {
		t.Run(tc.name, func(t *testing.T) {
			card := tc.card
			if got := videoCopyReconstructRefused(t.Context(), files, &card); got != tc.want {
				t.Fatalf("videoCopyReconstructRefused() = %v, want %v", got, tc.want)
			}
		})
	}
}
