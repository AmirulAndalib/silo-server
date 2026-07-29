package main

import (
	"github.com/Silo-Server/silo-server/internal/api"
	"github.com/Silo-Server/silo-server/internal/audiobooks"
	"github.com/Silo-Server/silo-server/internal/jellycompat"
	"github.com/Silo-Server/silo-server/internal/transfers"
)

// transferRegistryComposition owns the one process-local registry and keeps
// independently constructed protocol surfaces from accidentally diverging.
type transferRegistryComposition struct {
	registry *transfers.Registry
}

func newTransferRegistryComposition() *transferRegistryComposition {
	return &transferRegistryComposition{registry: transfers.New()}
}

func (c *transferRegistryComposition) wireAPI(deps *api.Dependencies) {
	deps.TransferRegistry = c.registry
}

func (c *transferRegistryComposition) wireJellycompat(deps *jellycompat.Dependencies) {
	deps.TransferRegistry = c.registry
}

func (c *transferRegistryComposition) wireABS(deps *audiobooks.ABSHandlerDeps) {
	deps.Transfers = c.registry
}
