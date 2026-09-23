// Package streamlocation classifies a playback request using the server's
// trusted client-IP and network-access middleware results.
package streamlocation

import (
	"context"
	"net"

	"github.com/Silo-Server/silo-server/internal/clientip"
	"github.com/Silo-Server/silo-server/internal/netaccess"
)

// IsRemote fails closed to remote when the resolved client address is absent
// or invalid. A validated network-access provider is remote even when its
// proxy connects from a private address.
func IsRemote(ctx context.Context) bool {
	if !netaccess.PathFromContext(ctx).IsDefault() {
		return true
	}
	ip := net.ParseIP(clientip.FromContext(ctx))
	if ip == nil {
		return true
	}
	return !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
}

// BitrateCap selects the administrator ceiling for this request's network
// location. The caller persists the selected value with the playback session.
func BitrateCap(ctx context.Context, localKbps, remoteKbps int) int {
	if IsRemote(ctx) {
		return remoteKbps
	}
	return localKbps
}
