package tv.onscreen.android.data.model

import com.squareup.moshi.JsonClass

/**
 * Server capabilities — feature flags, supported codecs, and operational
 * limits. Returned by GET /api/v1/system/capabilities.
 *
 * The server promises forward-compatibility on this shape: v2.x minor
 * releases may add fields but won't remove or rename them. Moshi's
 * loose decoding (default values per field) plus this contract means
 * a TV running an older binary against a newer server still gets a
 * usable subset.
 *
 * Per-flag rationale lives on the server's openapi.yaml — the
 * feature flags that AND-gate against runtime tool probes (`trickplay`,
 * `subtitles_ocr`, `intro_markers`, `backup`) reflect what *actually*
 * works on the box, not what the codebase intends to support.
 */
@JsonClass(generateAdapter = true)
data class Capabilities(
    val server: ServerInfo = ServerInfo(),
    val features: CapabilitiesFeatures = CapabilitiesFeatures(),
    val codecs: CapabilitiesCodecs = CapabilitiesCodecs(),
    val limits: CapabilitiesLimits = CapabilitiesLimits(),
    val discovery: CapabilitiesDiscovery = CapabilitiesDiscovery(),
)

@JsonClass(generateAdapter = true)
data class ServerInfo(
    val name: String = "",
    val machine_id: String = "",
    val version: String = "",
    val api_version: String = "",
)

@JsonClass(generateAdapter = true)
data class CapabilitiesFeatures(
    val transcode: Boolean = false,
    val trickplay: Boolean = false,
    val subtitles_external: Boolean = false,
    val subtitles_ocr: Boolean = false,
    val oidc: Boolean = false,
    val ldap: Boolean = false,
    val device_pairing: Boolean = false,
    val plugins: Boolean = false,
    val backup: Boolean = false,
    val people_credits: Boolean = false,
    val photos: Boolean = false,
    val music: Boolean = false,
    val webhooks: Boolean = false,
    val notifications: Boolean = false,
    val requests: Boolean = false,
    val live_tv: Boolean = false,
    val dvr: Boolean = false,
    val lyrics: Boolean = false,
    val intro_markers: Boolean = false,
    val chapters: Boolean = false,
    // web_downloads is server-side admin posture for the watch-page
    // Download button. TV clients don't surface it (no save-as on a
    // stationary set-top device) but the field is decoded so it
    // doesn't trip the strictness setting.
    val web_downloads: Boolean = false,
)

@JsonClass(generateAdapter = true)
data class CapabilitiesCodecs(
    val video: List<String> = emptyList(),
    val audio: List<String> = emptyList(),
    val containers: List<String> = emptyList(),
    val hardware: List<String> = emptyList(),
    val hdr_tonemap: Boolean = false,
)

@JsonClass(generateAdapter = true)
data class CapabilitiesLimits(
    val max_upload_bytes: Long = 0L,
    val max_transcode_bitrate_kbps: Int = 0,
    val max_transcode_width: Int = 0,
    val max_transcode_height: Int = 0,
    val max_concurrent_transcodes: Int = 0,
    val live_tv_tune_count: Int = 0,
)

@JsonClass(generateAdapter = true)
data class CapabilitiesDiscovery(
    val udp_port: Int = 0,
)
