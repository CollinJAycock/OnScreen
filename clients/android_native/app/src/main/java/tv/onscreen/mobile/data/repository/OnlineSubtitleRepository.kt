package tv.onscreen.mobile.data.repository

import tv.onscreen.mobile.data.api.OnScreenApi
import tv.onscreen.mobile.data.model.OnlineSubtitle
import tv.onscreen.mobile.data.model.SubtitleDownloadRequest
import javax.inject.Inject
import javax.inject.Singleton

/** OpenSubtitles search + download. Both go through the existing
 *  server-side proxy — the API key + rate-limit lives on the
 *  operator's box, never in client storage. */
@Singleton
open class OnlineSubtitleRepository @Inject constructor(
    private val api: OnScreenApi,
) {
    open suspend fun search(itemId: String, lang: String? = null, query: String? = null): List<OnlineSubtitle> =
        api.searchOnlineSubtitles(itemId, lang, query).data

    open suspend fun download(itemId: String, fileId: String, candidate: OnlineSubtitle) {
        api.downloadOnlineSubtitle(
            id = itemId,
            body = SubtitleDownloadRequest(
                file_id = fileId,
                provider_file_id = candidate.provider_file_id,
                language = candidate.language,
                title = candidate.file_name,
                hearing_impaired = candidate.hearing_impaired,
                rating = candidate.rating,
                download_count = candidate.download_count,
            ),
        )
    }
}
