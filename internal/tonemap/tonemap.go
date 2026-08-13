// Package tonemap owns HDR-to-SDR source classification and FFmpeg execution
// capability probing shared by playback transcodes and other media workers.
package tonemap

import (
	"bytes"
	"strings"

	"github.com/Silo-Server/silo-server/internal/models"
)

const (
	SoftwareFilterBT2390 = "tonemapx"
	SoftwareFilterHable  = "tonemap"
	HardwareFilterVAAPI  = "tonemap_vaapi"
	HardwareFilterCUDA   = "tonemap_cuda"

	BackendSoftware = "software"
	BackendQSV      = "qsv"
	BackendVAAPI    = "vaapi"
	BackendNVENC    = "nvenc"

	DynamicRangeHDRUnknown  = "hdr_unknown"
	DynamicRangeSDR         = "sdr"
	DynamicRangeDolbyVision = "dolby_vision"
	DynamicRangeHDR10Plus   = "hdr10_plus"
	DynamicRangeHLG         = "hlg"
	DynamicRangeHDR10       = "hdr10"
	ColorTransferHLG        = "arib-std-b67"
	ColorTransferPQ         = "smpte2084"

	colorBT709             = "bt709"
	colorBT2020            = "bt2020"
	colorBT2020NC          = "bt2020nc"
	colorUnknown           = "unknown"
	colorUnspecified       = "unspecified"
	defaultDRIRenderDevice = "/dev/dri/renderD128"
	ffmpegHideBannerArg    = "-hide_banner"
	ffmpegLogLevelArg      = "-loglevel"
	ffmpegErrorLogLevel    = "error"
	codecHEVC              = "hevc"
)

type Mode string

const (
	ModeHardware Mode = "hardware"
	ModeSoftware Mode = BackendSoftware
)

type Policy string

const (
	PolicyNone                 Policy = "none"
	PolicyHardwareOnly         Policy = "hardware_only"
	PolicySoftwareOnly         Policy = "software_only"
	PolicyHardwareThenSoftware Policy = "hardware_then_software"
)

func NewPolicy(hardwareEnabled, softwareEnabled bool) Policy {
	switch {
	case hardwareEnabled && softwareEnabled:
		return PolicyHardwareThenSoftware
	case hardwareEnabled:
		return PolicyHardwareOnly
	case softwareEnabled:
		return PolicySoftwareOnly
	default:
		return PolicyNone
	}
}

func (p Policy) Allows(mode Mode) bool {
	switch p {
	case PolicyHardwareThenSoftware:
		return mode == ModeHardware || mode == ModeSoftware
	case PolicyHardwareOnly:
		return mode == ModeHardware
	case PolicySoftwareOnly:
		return mode == ModeSoftware
	default:
		return false
	}
}

type SourceKind string

const (
	SourcePQ        SourceKind = "pq"
	SourceHLG       SourceKind = DynamicRangeHLG
	SourceHLGBT709  SourceKind = "hlg_bt709"
	SourceSDRBT709  SourceKind = "sdr_bt709"
	SourceSDRBT2020 SourceKind = "sdr_bt2020"
)

type SourceMetadata struct {
	DynamicRange        string
	DVProfile           int
	DVBLCompatID        int
	DVConfigPresent     bool
	DVBLCompatIDPresent bool
	DVBLPresent         bool
	DVRPUPresent        bool
	ColorRange          string
	ColorPrimaries      string
	ColorTransfer       string
	ColorSpace          string
}

// SourceResolution is the safe execution classification for one HDR source.
// A non-empty Kind with PreflightRequired is a candidate, not permission to
// execute: the selected node must validate decoded frames before publishing
// output. An empty Kind is a hard rejection.
type SourceResolution struct {
	Kind              SourceKind
	PreflightRequired bool
}

func MetadataForFile(file *models.MediaFile) SourceMetadata {
	if file == nil {
		return SourceMetadata{}
	}
	if len(file.VideoTracks) == 0 {
		if file.HDR {
			return SourceMetadata{DynamicRange: DynamicRangeHDRUnknown}
		}
		return SourceMetadata{DynamicRange: DynamicRangeSDR}
	}
	track := file.VideoTracks[0]
	dynamicRange := DynamicRangeForVideoTrack(track)
	if dynamicRange == "" && file.HDR {
		dynamicRange = DynamicRangeHDRUnknown
	}
	return SourceMetadata{
		DynamicRange:        dynamicRange,
		DVProfile:           track.DVProfile,
		DVBLCompatID:        track.DVBLCompatID,
		DVConfigPresent:     track.DVConfigPresent,
		DVBLCompatIDPresent: track.DVBLCompatIDPresent,
		DVBLPresent:         track.DVBLPresent,
		DVRPUPresent:        track.DVRPUPresent,
		ColorRange:          track.ColorRange,
		ColorPrimaries:      track.ColorPrimaries,
		ColorTransfer:       track.ColorTransfer,
		ColorSpace:          track.ColorSpace,
	}
}

// NeedsToneMap preserves the broad chapter-thumbnail policy gate: thumbnails
// may attempt best-effort conversion for incomplete legacy HDR metadata, while
// full video transcodes require ResolveSource to return a safe base or a
// candidate that passes executor preflight.
func NeedsToneMap(file *models.MediaFile) bool {
	if file == nil {
		return false
	}
	if file.HDR {
		return true
	}
	for _, track := range file.VideoTracks {
		if strings.TrimSpace(track.DolbyVision) != "" {
			return true
		}
	}
	return false
}

func DynamicRangeForVideoTrack(track models.VideoTrack) string {
	if track.DVProfile > 0 || strings.Contains(strings.ToLower(track.VideoRangeType), "dovi") || strings.Contains(strings.ToLower(track.DolbyVision), "dolby") {
		return DynamicRangeDolbyVision
	}
	if track.HDR10Plus || strings.Contains(strings.ToLower(track.VideoRangeType), "hdr10+") {
		return DynamicRangeHDR10Plus
	}
	joined := strings.ToLower(strings.Join([]string{track.VideoRange, track.VideoRangeType, track.ColorTransfer}, " "))
	if strings.Contains(joined, DynamicRangeHLG) || strings.Contains(joined, ColorTransferHLG) {
		return DynamicRangeHLG
	}
	if strings.Contains(joined, "hdr") || strings.Contains(joined, ColorTransferPQ) || strings.Contains(joined, "pq") {
		return DynamicRangeHDR10
	}
	if strings.TrimSpace(joined) == "" {
		return ""
	}
	return DynamicRangeSDR
}

func ResolveSource(source SourceMetadata) SourceResolution {
	dynamicRange := strings.ToLower(strings.TrimSpace(source.DynamicRange))
	var candidate SourceKind
	preflight := false
	switch dynamicRange {
	case DynamicRangeHDR10, DynamicRangeHDR10Plus:
		candidate = SourcePQ
	case DynamicRangeHLG:
		candidate = SourceHLG
	case DynamicRangeDolbyVision:
		// Profile 5 and an explicitly signaled compatibility id 0 carry a
		// Dolby-proprietary base signal. Treating either as ordinary PQ is the
		// purple/green-output failure this resolver is designed to prevent.
		if source.DVProfile == 5 || source.DVBLCompatIDPresent && source.DVBLCompatID == 0 {
			return SourceResolution{}
		}
		if source.DVConfigPresent && !source.DVBLPresent {
			return SourceResolution{}
		}
		candidate = sourceKindForCompatibilityID(source.DVBLCompatID)
		if candidate == "" {
			candidate = sourceKindFromColorMetadata(source)
			preflight = candidate != ""
		} else if !source.DVBLCompatIDPresent || !standardProfileCompatibility(source.DVProfile, source.DVBLCompatID) {
			preflight = true
		}
	case DynamicRangeHDRUnknown:
		candidate = sourceKindFromColorMetadata(source)
		preflight = candidate != ""
	default:
		return SourceResolution{}
	}
	if candidate == "" {
		return SourceResolution{}
	}
	complete, compatible := sourceMetadataCompatibility(candidate, source)
	if !compatible || !complete {
		preflight = true
	}
	return SourceResolution{Kind: candidate, PreflightRequired: preflight}
}

func ClassifySource(source SourceMetadata) SourceKind {
	resolution := ResolveSource(source)
	if resolution.PreflightRequired {
		return ""
	}
	return resolution.Kind
}

func sourceKindForCompatibilityID(id int) SourceKind {
	switch id {
	case 1, 6:
		return SourcePQ
	case 2:
		return SourceSDRBT709
	case 3:
		return SourceHLGBT709
	case 4:
		return SourceHLG
	case 5:
		return SourceSDRBT2020
	}
	return ""
}

func standardProfileCompatibility(profile, compatibilityID int) bool {
	switch profile {
	case 4:
		return compatibilityID == 2
	case 7:
		return compatibilityID == 6
	case 8:
		return compatibilityID == 1 || compatibilityID == 2 || compatibilityID == 4
	case 9:
		return compatibilityID == 2
	case 10:
		return compatibilityID == 1 || compatibilityID == 2 || compatibilityID == 4
	case 0:
		return false
	default:
		// Retired profiles and legacy compatibility ids are accepted only
		// after source preflight, not from their catalog tags alone.
		return false
	}
}

func sourceKindFromColorMetadata(source SourceMetadata) SourceKind {
	transfer := normalizeColorValue(source.ColorTransfer)
	primaries := normalizeColorValue(source.ColorPrimaries)
	switch {
	case transferIsPQ(transfer):
		return SourcePQ
	case transferIsHLG(transfer) && colorIsBT709(primaries):
		return SourceHLGBT709
	case transferIsHLG(transfer):
		return SourceHLG
	case transferIsSDR(transfer) && colorIsBT2020(primaries):
		return SourceSDRBT2020
	case transferIsSDR(transfer) && colorIsBT709(primaries):
		return SourceSDRBT709
	}
	return ""
}

func sourceMetadataCompatibility(kind SourceKind, source SourceMetadata) (complete, compatible bool) {
	values := []string{source.ColorRange, source.ColorPrimaries, source.ColorTransfer, source.ColorSpace}
	complete = true
	for _, value := range values {
		if normalizeColorValue(value) == "" || normalizeColorValue(value) == colorUnknown || normalizeColorValue(value) == colorUnspecified {
			complete = false
		}
	}
	rangeValue := normalizeColorValue(source.ColorRange)
	if rangeValue != "" && rangeValue != colorUnknown && rangeValue != colorUnspecified && !rangeIsLimited(rangeValue) {
		return complete, false
	}
	primaries := normalizeColorValue(source.ColorPrimaries)
	transfer := normalizeColorValue(source.ColorTransfer)
	matrix := normalizeColorValue(source.ColorSpace)
	return complete,
		colorValueMatchesPrimaries(kind, primaries) &&
			colorValueMatchesTransfer(kind, transfer) &&
			colorValueMatchesMatrix(kind, matrix)
}

func normalizeColorValue(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func colorValueMatchesPrimaries(kind SourceKind, value string) bool {
	if value == "" || value == colorUnknown || value == colorUnspecified {
		return true
	}
	if kind == SourceHLGBT709 || kind == SourceSDRBT709 {
		return colorIsBT709(value)
	}
	return colorIsBT2020(value)
}

func colorValueMatchesTransfer(kind SourceKind, value string) bool {
	if value == "" || value == colorUnknown || value == colorUnspecified {
		return true
	}
	switch kind {
	case SourcePQ:
		return transferIsPQ(value)
	case SourceHLG, SourceHLGBT709:
		return transferIsHLG(value)
	case SourceSDRBT709, SourceSDRBT2020:
		return transferIsSDR(value)
	default:
		return false
	}
}

func colorValueMatchesMatrix(kind SourceKind, value string) bool {
	if value == "" || value == colorUnknown || value == colorUnspecified {
		return true
	}
	if kind == SourceSDRBT709 {
		return colorIsBT709(value)
	}
	if kind == SourceHLGBT709 {
		return colorIsBT709(value) || colorIsBT2020(value)
	}
	return colorIsBT2020(value)
}

func transferIsPQ(value string) bool {
	return strings.Contains(value, ColorTransferPQ) || value == "pq"
}

func transferIsHLG(value string) bool {
	return strings.Contains(value, ColorTransferHLG) || value == DynamicRangeHLG
}

func transferIsSDR(value string) bool {
	return value == colorBT709 || value == "bt1886" || value == "bt470bg" || value == "gamma28"
}

func colorIsBT709(value string) bool {
	return strings.Contains(value, colorBT709)
}

func colorIsBT2020(value string) bool {
	return strings.Contains(value, colorBT2020)
}

func rangeIsLimited(value string) bool {
	return value == "tv" || value == "mpeg" || value == "limited"
}

func SourceKindFor(dynamicRange string, dvBLCompatID int) SourceKind {
	switch strings.ToLower(strings.TrimSpace(dynamicRange)) {
	case DynamicRangeHDR10, DynamicRangeHDR10Plus:
		return SourcePQ
	case DynamicRangeHLG:
		return SourceHLG
	case DynamicRangeDolbyVision:
		return sourceKindForCompatibilityID(dvBLCompatID)
	}
	return ""
}

func SourceTransfer(kind SourceKind) string {
	if kind == SourceHLG || kind == SourceHLGBT709 {
		return ColorTransferHLG
	}
	if kind == SourceSDRBT709 || kind == SourceSDRBT2020 {
		return colorBT709
	}
	return ColorTransferPQ
}

func SourcePrimaries(kind SourceKind) string {
	if kind == SourceHLGBT709 || kind == SourceSDRBT709 {
		return colorBT709
	}
	return colorBT2020
}

func SourceMatrix(kind SourceKind) string {
	if kind == SourceHLGBT709 || kind == SourceSDRBT709 {
		return colorBT709
	}
	return colorBT2020NC
}

func IsSDRSource(kind SourceKind) bool {
	return kind == SourceSDRBT709 || kind == SourceSDRBT2020
}

func AllSourceKinds() []SourceKind {
	return []SourceKind{SourcePQ, SourceHLG, SourceHLGBT709, SourceSDRBT709, SourceSDRBT2020}
}

func ValidSourceKind(kind SourceKind) bool {
	for _, candidate := range AllSourceKinds() {
		if candidate == kind {
			return true
		}
	}
	return false
}

type Capability struct {
	Mode        Mode         `json:"mode"`
	Backend     string       `json:"backend"`
	Filter      string       `json:"filter"`
	SourceKinds []SourceKind `json:"source_kinds"`
}

type Capabilities []Capability

func (c Capabilities) Supports(mode Mode, kind SourceKind) bool {
	for _, capability := range c {
		if capability.Mode != mode {
			continue
		}
		for _, supported := range capability.SourceKinds {
			if supported == kind {
				return true
			}
		}
	}
	return false
}

func (c Capabilities) FilterFor(mode Mode, kind SourceKind) string {
	for _, capability := range c {
		if capability.Mode == mode && slicesContain(capability.SourceKinds, kind) {
			return capability.Filter
		}
	}
	return ""
}

func (c Capabilities) BackendFor(mode Mode, kind SourceKind) string {
	for _, capability := range c {
		if capability.Mode == mode && slicesContain(capability.SourceKinds, kind) {
			return capability.Backend
		}
	}
	return ""
}

func slicesContain(values []SourceKind, want SourceKind) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (c Capabilities) SupportsPolicy(policy Policy, kind SourceKind) bool {
	return policy.Allows(ModeHardware) && c.Supports(ModeHardware, kind) ||
		policy.Allows(ModeSoftware) && c.Supports(ModeSoftware, kind)
}

func (c Capabilities) PreferredMode(policy Policy, kind SourceKind) Mode {
	if policy.Allows(ModeHardware) && c.Supports(ModeHardware, kind) {
		return ModeHardware
	}
	if policy.Allows(ModeSoftware) && c.Supports(ModeSoftware, kind) {
		return ModeSoftware
	}
	return ""
}

func SourceParameters(kind SourceKind) string {
	return "setparams=range=tv:color_primaries=" + SourcePrimaries(kind) + ":color_trc=" + SourceTransfer(kind) + ":colorspace=" + SourceMatrix(kind)
}

func SoftwareFilter(kind SourceKind, filterName string) string {
	if IsSDRSource(kind) {
		return SourceParameters(kind) +
			",zscale=p=bt709:t=bt709:m=bt709:r=tv,format=yuv420p," + HDRMetadataRemovalFilter()
	}
	algorithm := "tonemap=hable"
	if filterName == SoftwareFilterBT2390 {
		algorithm = "tonemapx=tonemap=bt2390"
	}
	return SourceParameters(kind) +
		",zscale=t=linear:npl=100,format=gbrpf32le," + algorithm +
		",zscale=p=bt709:t=bt709:m=bt709:r=tv,format=yuv420p," + HDRMetadataRemovalFilter()
}

// SelectSoftwareFilter inspects an FFmpeg -filters listing and returns the
// preferred software tone-map filter. It is intentionally only a listing
// probe; playback capability advertising additionally runs an encode smoke
// test, while chapter thumbnails preserve their existing best-effort policy.
func SelectSoftwareFilter(output []byte) (filter string, hasZScale bool) {
	hasZScale = FilterListingHas(output, "zscale")
	if !hasZScale {
		return "", false
	}
	if FilterListingHas(output, SoftwareFilterBT2390) {
		return SoftwareFilterBT2390, true
	}
	if FilterListingHas(output, SoftwareFilterHable) {
		return SoftwareFilterHable, true
	}
	return "", true
}

func FilterListingHas(output []byte, name string) bool {
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		fields := bytes.Fields(line)
		if len(fields) >= 3 && bytes.Contains(fields[2], []byte("->")) && strings.EqualFold(string(fields[1]), name) {
			return true
		}
	}
	return false
}

func VAAPIFilter(kind SourceKind) string {
	if IsSDRSource(kind) {
		return SourceParameters(kind) + "," + vaapiSDRFilter()
	}
	return SourceParameters(kind) + "," + vaapiToneMapFilter()
}

func vaapiToneMapFilter() string {
	return HardwareFilterVAAPI + "=format=nv12:p=bt709:t=bt709:m=bt709"
}

func vaapiSDRFilter() string {
	return "scale_vaapi=format=nv12:out_color_primaries=bt709:out_color_transfer=bt709:out_color_matrix=bt709:out_range=tv"
}

func CUDAFilter() string {
	return HardwareFilterCUDA + "=tonemap=bt2390:format=nv12:p=bt709:t=bt709:m=bt709:r=tv:apply_dovi=0"
}

// QSVInteropFilter normalizes the VAAPI tone-map surface before deriving the
// QSV encoder surface. The extra scale_vaapi stage is required by Intel's
// driver for real decoded HEVC frames even when tonemap_vaapi already emitted
// NV12; omitting it leaves FFmpeg trying an unsupported software auto-scale.
func QSVInteropFilter() string {
	return "scale_vaapi=format=nv12,hwmap=derive_device=qsv:mode=read+write,format=qsv"
}

func qsvVAAPIInitDevice(device string) string {
	return "vaapi=va:" + device + ",driver=iHD,kernel_driver=i915,vendor_id=0x8086"
}

func HDRMetadataRemovalFilter() string {
	return strings.Join([]string{
		"sidedata=mode=delete:type=MASTERING_DISPLAY_METADATA",
		"sidedata=mode=delete:type=CONTENT_LIGHT_LEVEL",
		"sidedata=mode=delete:type=DYNAMIC_HDR_PLUS",
		"sidedata=mode=delete:type=DOVI_RPU_BUFFER",
		"sidedata=mode=delete:type=DOVI_METADATA",
	}, ",")
}
