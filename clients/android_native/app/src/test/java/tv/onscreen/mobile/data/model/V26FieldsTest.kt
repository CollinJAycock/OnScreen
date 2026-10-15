package tv.onscreen.mobile.data.model

import com.google.common.truth.Truth.assertThat
import com.squareup.moshi.Moshi
import com.squareup.moshi.Types
import org.junit.Test
import tv.onscreen.mobile.data.api.ApiResponse

/**
 * The additive v2.6 item fields (internal/api/v1/items.go) decode, and an
 * older server that omits every one of them still parses.
 */
class V26FieldsTest {

    private val moshi = Moshi.Builder().build()

    private fun detail(json: String): ItemDetail {
        val type = Types.newParameterizedType(ApiResponse::class.java, ItemDetail::class.java)
        return moshi.adapter<ApiResponse<ItemDetail>>(type).fromJson(json)!!.data
    }

    @Test
    fun `v2_6 detail fields decode`() {
        val d = detail(
            """{"data":{"id":"e1","library_id":"l","title":"Pilot","type":"episode",
            "index":1,"index_end":2,"genres":[],"view_offset_ms":0,"is_favorite":false,"updated_at":1,
            "logo_path":"items/e1/logo.png","banner_path":"b.jpg","disc_art_path":"d.png",
            "studios":[{"id":"s1","name":"HBO","kind":"network"}],
            "files":[{"id":"f1","stream_url":"/media/stream/f1","faststart":false,
              "edition":"Director's Cut","part_index":1,"audio_streams":[],"subtitle_streams":[],
              "chapters":[{"title":"One","start_ms":0,"end_ms":10,"thumb_url":"/trickplay/e1/chapter_001.jpg"}]}],
            "optimized_versions":[{"id":"ov1","file_id":"f1","profile":"1080p","label":"1080p",
              "container":"mp4","video_codec":"h264","audio_codec":"aac","audio_channels":2,
              "stream_url":"/media/optimized/ov1","stream_token":"t"}]}}""",
        )
        assertThat(d.index_end).isEqualTo(2)
        assertThat(d.logo_path).isEqualTo("items/e1/logo.png")
        assertThat(d.banner_path).isEqualTo("b.jpg")
        assertThat(d.disc_art_path).isEqualTo("d.png")
        assertThat(d.studios.map { it.name }).containsExactly("HBO")
        val f = d.files.single()
        assertThat(f.edition).isEqualTo("Director's Cut")
        assertThat(f.part_index).isEqualTo(1)
        assertThat(f.chapters.single().thumb_url).isEqualTo("/trickplay/e1/chapter_001.jpg")
        assertThat(d.optimized_versions.single().stream_url).isEqualTo("/media/optimized/ov1")
    }

    @Test
    fun `an older server's detail parses with every v2_6 field absent`() {
        val d = detail(
            """{"data":{"id":"m","library_id":"l","title":"Old","type":"movie","genres":[],
            "view_offset_ms":0,"is_favorite":false,"updated_at":1,
            "files":[{"id":"f","stream_url":"/s","faststart":true,"audio_streams":[],"subtitle_streams":[],
              "chapters":[{"title":"","start_ms":0,"end_ms":1}]}]}}""",
        )
        assertThat(d.index_end).isNull()
        assertThat(d.logo_path).isNull()
        assertThat(d.studios).isEmpty()
        assertThat(d.optimized_versions).isEmpty()
        assertThat(d.files.single().edition).isNull()
        assertThat(d.files.single().part_index).isNull()
        assertThat(d.files.single().chapters.single().thumb_url).isNull()
    }

    @Test
    fun `the play decision carries an optimized version and the source's verdict`() {
        val type = Types.newParameterizedType(ApiResponse::class.java, PlaybackDecision::class.java)
        val dec = moshi.adapter<ApiResponse<PlaybackDecision>>(type).fromJson(
            """{"data":{"decision":"directPlay","file_id":"f1","reason":"optimized_version",
            "optimized_version":{"id":"ov1","file_id":"f1","profile":"720p","label":"720p","container":"mp4",
              "video_codec":"h264","audio_codec":"aac","audio_channels":2,"stream_url":"/media/optimized/ov1"},
            "source_decision":"transcode","source_reason":"remote_bitrate_limit"}}""",
        )!!.data
        assertThat(dec.reason).isEqualTo("optimized_version")
        assertThat(dec.optimized_version?.id).isEqualTo("ov1")
        assertThat(dec.source_decision).isEqualTo("transcode")
        assertThat(dec.source_reason).isEqualTo("remote_bitrate_limit")
    }

    @Test
    fun `preferences carry the quality profile`() {
        val type = Types.newParameterizedType(ApiResponse::class.java, UserPreferences::class.java)
        val p = moshi.adapter<ApiResponse<UserPreferences>>(type).fromJson(
            """{"data":{"preferred_audio_lang":null,"preferred_subtitle_lang":null,"max_content_rating":null,
            "max_video_height":1080,"max_video_bitrate_kbps":8000,"max_audio_bitrate_kbps":192,
            "preferred_video_codec":"hevc","forced_subtitles_only":true,"has_pin":false}}""",
        )!!.data
        assertThat(p.max_video_height).isEqualTo(1080)
        assertThat(p.max_video_bitrate_kbps).isEqualTo(8000)
        assertThat(p.max_audio_bitrate_kbps).isEqualTo(192)
        assertThat(p.preferred_video_codec).isEqualTo("hevc")
        assertThat(p.forced_subtitles_only).isTrue()
    }
}
