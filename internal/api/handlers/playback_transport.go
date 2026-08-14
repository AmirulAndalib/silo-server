package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/tonemap"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
)

// startLocalPlaybackTransport is the shared local ffmpeg launch primitive for
// legacy and protocol-v3 orchestration. Callers retain ownership of lifecycle
// locking and decide whether registration is immediate or transactionally
// staged.
func (h *PlaybackHandler) startLocalPlaybackTransport(ctx context.Context, opts playback.TranscodeOpts) (*playback.TranscodeSession, error) {
	return playback.StartTranscode(context.WithoutCancel(ctx), opts)
}

// startRemotePlaybackTransport is the shared remote-node launch primitive.
// It returns the node's HTTP status separately so legacy and v3 can preserve
// their existing public error envelopes while executing identical transport
// startup and response parsing.
func (h *PlaybackHandler) startRemotePlaybackTransport(ctx context.Context, nodeURL string, request transcodenode.TranscodeStartRequest) (transcodenode.TranscodeStartResponse, int, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return transcodenode.TranscodeStartResponse{}, 0, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, h.remotePlaybackTransportTimeout(nodeURL, request))
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestCtx, http.MethodPost, nodeURL+"/transcode/start", bytes.NewReader(body))
	if err != nil {
		return transcodenode.TranscodeStartResponse{}, 0, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+h.JWTSecret)
	response, err := http.DefaultClient.Do(httpRequest)
	if err != nil {
		return transcodenode.TranscodeStartResponse{}, 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		// Drain the (small) error body so the transport can reuse the
		// connection instead of tearing it down on every failed start.
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return transcodenode.TranscodeStartResponse{}, response.StatusCode, nil
	}
	var result transcodenode.TranscodeStartResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		slog.WarnContext(ctx, "remote transcode start response decode failed", "component", "api", "node", nodeURL, "error", err)
	}
	return result, response.StatusCode, nil
}

func (h *PlaybackHandler) remotePlaybackTransportTimeout(nodeURL string, request transcodenode.TranscodeStartRequest) time.Duration {
	if request.ToneMapMode == "" {
		return 20 * time.Second
	}
	timeout := h.remoteToneMapProbeTimeoutV3(nodeURL)
	if request.ToneMapPreflightRequired {
		timeout += tonemap.SourcePreflightTimeout(request.TotalDuration)
	}
	if request.RequireReady {
		timeout += transcodenode.TranscodeStartReadinessTimeout
	}
	return timeout
}

func fetchRemoteTranscodeCapabilities(ctx context.Context, nodeURL, jwtSecret string) (playback.HWAccelInfo, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(nodeURL, "/")+"/hw-capabilities", nil)
	if err != nil {
		return playback.HWAccelInfo{}, err
	}
	request.Header.Set("Authorization", "Bearer "+jwtSecret)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return playback.HWAccelInfo{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return playback.HWAccelInfo{}, fmt.Errorf("node returned %d", response.StatusCode)
	}
	var info playback.HWAccelInfo
	if err := json.NewDecoder(response.Body).Decode(&info); err != nil {
		return playback.HWAccelInfo{}, err
	}
	info.Source = "transcode_node"
	info.NodeURL = nodeURL
	return info, nil
}
