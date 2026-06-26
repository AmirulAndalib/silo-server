package providerregistry

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/subtitles"
	"github.com/Silo-Server/silo-server/internal/subtitles/opensubtitles"
	"github.com/Silo-Server/silo-server/internal/subtitles/subdl"
	"github.com/Silo-Server/silo-server/internal/subtitles/subsource"
)

// NewManagerFromRepository builds a subtitle manager and registers every
// enabled provider config that has the credentials needed for that provider.
func NewManagerFromRepository(
	ctx context.Context,
	repo subtitles.Repository,
	s3 subtitles.S3Client,
	bucket string,
) (*subtitles.Manager, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	manager := subtitles.NewManager(repo, s3, bucket)
	configs, err := repo.ListProviderConfigs(ctx)
	if err != nil {
		return manager, err
	}
	RegisterEnabledProviders(manager, configs)
	return manager, nil
}

func RegisterEnabledProviders(manager *subtitles.Manager, configs []subtitles.ProviderConfig) {
	if manager == nil {
		return
	}
	for _, cfg := range configs {
		if !cfg.Enabled {
			continue
		}
		switch cfg.ProviderName {
		case "opensubtitles":
			if cfg.Username == "" || cfg.Password == "" {
				continue
			}
			manager.RegisterProvider(opensubtitles.New(opensubtitles.Config{
				Username: cfg.Username,
				Password: cfg.Password,
			}))
		case "subdl":
			if cfg.APIKey == "" {
				continue
			}
			manager.RegisterProvider(subdl.New(subdl.Config{APIKey: cfg.APIKey}))
		case "subsource":
			if cfg.APIKey == "" {
				continue
			}
			manager.RegisterProvider(subsource.New(subsource.Config{APIKey: cfg.APIKey}))
		}
	}
}
