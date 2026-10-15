package tv.onscreen.mobile.ui.settings

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import retrofit2.HttpException
import tv.onscreen.mobile.data.api.apiError
import tv.onscreen.mobile.data.repository.PreferencesRepository
import javax.inject.Inject

data class PlaybackQualityUi(
    val loading: Boolean = true,
    /** The profile as the server holds it, by field (null = no limit). */
    val values: Map<QualityProfile.Field, String?> = emptyMap(),
    /** The load failed; the screen shows this instead of the options. */
    val loadError: String? = null,
    /** A save in flight. */
    val saving: Boolean = false,
    /** One-shot feedback for a toast ("Saved", or why a save failed). */
    val message: String? = null,
)

/**
 * Settings → Playback quality: the user's server-side quality profile, which
 * the server applies on every device. Each pick saves at once: a fresh read,
 * then PUT /users/me/quality-profile with the picked field changed and the
 * rest — forced_subtitles_only included — echoed from that read (the endpoint
 * replaces the whole profile).
 */
@HiltViewModel
class PlaybackQualityViewModel @Inject constructor(
    private val prefs: PreferencesRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(PlaybackQualityUi())
    val state: StateFlow<PlaybackQualityUi> = _state.asStateFlow()

    init { load() }

    fun load() {
        _state.update { it.copy(loading = true, loadError = null) }
        viewModelScope.launch {
            try {
                val p = prefs.fresh()
                _state.update { s ->
                    s.copy(loading = false, values = QualityProfile.Field.entries.associateWith { QualityProfile.valueOf(p, it) })
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                _state.update { it.copy(loading = false, loadError = "Couldn’t load your playback quality.") }
            }
        }
    }

    fun set(field: QualityProfile.Field, value: String?) {
        if (_state.value.saving || _state.value.values[field] == value) return
        _state.update { it.copy(saving = true) }
        viewModelScope.launch {
            try {
                val fresh = prefs.fresh()
                val body = QualityProfile.request(fresh, field, value)
                prefs.setQualityProfile(body)
                _state.update { s ->
                    s.copy(
                        saving = false,
                        message = "Playback quality saved",
                        values = mapOf(
                            QualityProfile.Field.HEIGHT to body.max_video_height?.toString(),
                            QualityProfile.Field.VIDEO_BITRATE to body.max_video_bitrate_kbps?.toString(),
                            QualityProfile.Field.AUDIO_BITRATE to body.max_audio_bitrate_kbps?.toString(),
                            QualityProfile.Field.CODEC to body.preferred_video_codec,
                        ),
                    )
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                _state.update { it.copy(saving = false, message = saveFailure(e)) }
            }
        }
    }

    fun consumeMessage() {
        _state.update { it.copy(message = null) }
    }

    private fun saveFailure(e: Exception): String {
        val http = e as? HttpException ?: return "Couldn’t save. Check your connection and try again."
        return when (http.code()) {
            400 -> http.apiError()?.message?.takeIf { it.isNotBlank() }
                ?.let { "That value isn’t allowed: $it" }
                ?: "That value isn’t allowed."
            404 -> "This server doesn’t support a playback quality setting."
            else -> "Couldn’t save (HTTP ${http.code()})."
        }
    }
}
