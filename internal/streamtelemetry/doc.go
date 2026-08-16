// Package streamtelemetry records local, observation-only telemetry for media
// responses.
//
// Observe starts a provisional per-request Observation. A handler promotes it
// with Attach only after it has loaded and authorized the canonical playback
// session or transfer owner. A request that never attaches cannot create logical
// activity; its accepted bytes are instead reported as unattributed. Released
// observations fold their final byte count into a retained LogicalSession or
// Transfer, so short requests that fit between collector sweeps are not lost.
//
// BytesAccepted means response body bytes accepted by the writer at the point
// where Observe is enrolled. It is wire bytes on bulk routes that bypass outer
// compression, and pre-compression bytes on compressible subtitle/font routes.
//
// P0b is deliberately local and observational. This package performs no
// admission, throttling, cutting, persistence, or distributed publication.
package streamtelemetry
