package jellycompat

import "github.com/Silo-Server/silo-server/internal/config"

func requireCompatWorkerRouting(handler *PlaybackHandler) {
	previous := handler.PlaybackConfig
	handler.PlaybackConfig = func() config.PlaybackConfig {
		var cfg config.PlaybackConfig
		if previous != nil {
			cfg = previous()
		}
		cfg.Routing = config.DefaultPlaybackRoutingPolicy()
		cfg.Routing.RemuxExecution = config.PlaybackExecutionWorkerOnly
		cfg.Routing.VideoTranscodeExecution = config.PlaybackExecutionWorkerOnly
		return cfg
	}
}
