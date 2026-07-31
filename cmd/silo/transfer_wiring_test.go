package main

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/api"
	"github.com/Silo-Server/silo-server/internal/audiobooks"
	"github.com/Silo-Server/silo-server/internal/jellycompat"
)

func TestTransferRegistryCompositionSharesOneInstance(t *testing.T) {
	composition := newTransferRegistryComposition(24)
	var apiDeps api.Dependencies
	var compatDeps jellycompat.Dependencies
	var absDeps audiobooks.ABSHandlerDeps

	composition.wireAPI(&apiDeps)
	composition.wireJellycompat(&compatDeps)
	composition.wireABS(&absDeps)

	if apiDeps.TransferRegistry == nil {
		t.Fatal("API/native/admin transfer registry is nil")
	}
	if got := apiDeps.TransferRegistry.MaxPerUser(); got != 24 {
		t.Fatalf("max per-user transfers = %d, want 24", got)
	}
	if apiDeps.TransferRegistry != compatDeps.TransferRegistry {
		t.Fatal("jellycompat received a different transfer registry")
	}
	if apiDeps.TransferRegistry != absDeps.Transfers {
		t.Fatal("ABS received a different transfer registry")
	}
}
