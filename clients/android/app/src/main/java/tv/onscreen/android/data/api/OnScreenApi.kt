package tv.onscreen.android.data.api

import retrofit2.Response
import retrofit2.http.*
import tv.onscreen.android.data.model.*

interface OnScreenApi {

    // ── Auth ────────────────────────────────────────────────────────────────

    @POST("api/v1/auth/login")
    suspend fun login(@Body body: LoginRequest): ApiResponse<TokenPair>

    /** Second step of a two-factor login — challenge token + code
     *  (or recovery code) in exchange for a real token pair. */
    @POST("api/v1/auth/totp/verify")
    suspend fun verifyTotp(@Body body: TotpVerifyRequest): ApiResponse<TokenPair>

    @POST("api/v1/auth/refresh")
    suspend fun refresh(@Body body: RefreshRequest): ApiResponse<TokenPair>

    /** Revoke a refresh token. The bearer is passed explicitly (matching the
     *  phone client) rather than left to AuthInterceptor's cache: the server's
     *  logout route runs under Optional auth, so WITHOUT valid claims it skips
     *  revocation and still answers 204 — the session survives a "successful"
     *  sign-out and the client cannot tell. Callers rotate the token first so
     *  the claims they present are live. */
    @POST("api/v1/auth/logout")
    suspend fun logout(
        @Body body: LogoutRequest,
        @Header("Authorization") authorization: String? = null,
    )

    /** Start a device-pairing session. The TV displays the returned
     *  PIN; the user signs in via the web at /pair on a phone /
     *  laptop and types the PIN to finish the link. */
    @POST("api/v1/auth/pair/code")
    suspend fun createPairCode(): ApiResponse<PairCodeResponse>

    /** Poll for completion. While pending the server replies 202
     *  with a {status, expires_at} body that Retrofit returns as
     *  isSuccessful=true with body=null (we treat 202 as "still
     *  pending"). Once claimed it returns 200 with a TokenPair.
     *  Authorization is the device_token from createPairCode, NOT
     *  the user bearer (the user isn't logged in yet at this point);
     *  pass it pre-formatted as "Bearer <token>". */
    @GET("api/v1/auth/pair/poll")
    suspend fun pollPairCode(
        @Header("Authorization") deviceTokenHeader: String,
    ): Response<PairPollResponse>

    // ── System (v2.2) ───────────────────────────────────────────────────────

    /** Server feature flags + codecs + limits. Public endpoint — no auth
     *  needed. Returns the v2.2.0 contract; future v2.x adds fields but
     *  doesn't remove them, and Moshi's loose decoding picks up new
     *  flags without a client release. TVs use this to gate UI for
     *  optional features (live_tv, dvr) that the operator may not have
     *  wired. */
    @GET("api/v1/system/capabilities")
    suspend fun getCapabilities(): ApiResponse<Capabilities>

    // ── Hub ─────────────────────────────────────────────────────────────────

    @GET("api/v1/hub")
    suspend fun getHub(): ApiResponse<HubData>

    // ── Libraries ───────────────────────────────────────────────────────────

    @GET("api/v1/libraries")
    suspend fun getLibraries(): ApiResponse<List<Library>>

    @GET("api/v1/libraries/{id}/genres")
    suspend fun getLibraryGenres(@Path("id") libraryId: String): ApiResponse<List<GenreCount>>

    @GET("api/v1/libraries/{id}/items")
    suspend fun getLibraryItems(
        @Path("id") libraryId: String,
        @Query("limit") limit: Int = 50,
        @Query("offset") offset: Int = 0,
        @Query("sort") sort: String? = null,
        @Query("sort_dir") sortDir: String? = null,
        @Query("genre") genre: String? = null,
        // v2.5: unwatched | in_progress | watched (null = all). Older servers
        // ignore the unknown param and return the unfiltered listing.
        @Query("watch") watch: String? = null,
    ): ApiListResponse<MediaItem>

    /** v2.5 "Surprise me": one random item the caller can see, under the same
     *  filters as the listing (sort ignored). 404 when nothing matches, 501
     *  when the server has no watch-state store wired. */
    @GET("api/v1/libraries/{id}/random")
    suspend fun getRandomLibraryItem(
        @Path("id") libraryId: String,
        @Query("genre") genre: String? = null,
        @Query("watch") watch: String? = null,
    ): ApiResponse<RandomLibraryItem>

    // ── Items ───────────────────────────────────────────────────────────────

    @GET("api/v1/items/{id}")
    suspend fun getItem(@Path("id") id: String): ApiResponse<ItemDetail>

    @GET("api/v1/items/{id}/children")
    suspend fun getChildren(
        @Path("id") id: String,
        @Query("limit") limit: Int = 200,
        @Query("offset") offset: Int = 0,
    ): ApiListResponse<ChildItem>

    @PUT("api/v1/items/{id}/progress")
    suspend fun updateProgress(
        @Path("id") id: String,
        @Body body: ProgressRequest,
    )

    @GET("api/v1/items/{id}/markers")
    suspend fun getMarkers(@Path("id") id: String): ApiListResponse<Marker>

    // ── Watch-status mirror (v2.2) ──────────────────────────────────────────
    // Plan to Watch / Watching / Completed / On Hold / Dropped — generic
    // across every type, not anime-only. Distinct from playback progress;
    // this is the user's explicit classification.

    /** Returns the current watch status for an item. Server (post-PR #27)
     *  always emits 200 with the struct; when nothing is set yet,
     *  `data.status` is the empty string. Consumers can treat `""` as
     *  the "no row yet" sentinel — same UX as null, no need for
     *  HTTP-code pattern matching. */
    @GET("api/v1/items/{id}/watch-status")
    suspend fun getWatchStatus(@Path("id") id: String): ApiResponse<WatchStatus>

    @PUT("api/v1/items/{id}/watch-status")
    suspend fun setWatchStatus(
        @Path("id") id: String,
        @Body body: WatchStatusUpdate,
    ): Response<Unit>

    @DELETE("api/v1/items/{id}/watch-status")
    suspend fun clearWatchStatus(@Path("id") id: String): Response<Unit>

    // ── Manual watch state (v2.5) ───────────────────────────────────────────
    // Played / unplayed marks (a show or season expands to its episodes),
    // hide-from-Continue-Watching, and which episode Play on a show / season
    // starts. The three writes answer 204 and share a ~60/min per-user rate
    // limit (429); a non-video type is 422. Unit return: Retrofit throws
    // HttpException on any non-2xx, so callers can branch on code().

    @POST("api/v1/items/{id}/watched")
    suspend fun markWatched(@Path("id") id: String)

    @DELETE("api/v1/items/{id}/watched")
    suspend fun markUnwatched(@Path("id") id: String)

    @POST("api/v1/items/{id}/dismiss-continue-watching")
    suspend fun dismissContinueWatching(@Path("id") id: String)

    /** Show or season only (other types: 422). */
    @GET("api/v1/items/{id}/up-next")
    suspend fun getUpNext(@Path("id") id: String): ApiResponse<UpNext>

    @GET("api/v1/items/{id}/trickplay")
    suspend fun getTrickplayStatus(@Path("id") id: String): ApiResponse<TrickplayStatus>

    // ── Report a problem (v2.5) ─────────────────────────────────────────────
    // Any user can flag an item they can see; admins work the reports on the
    // web's Library health page. POST answers 201 with the report, 409
    // ALREADY_REPORTED (an open report of that kind exists), 429
    // TOO_MANY_OPEN_ISSUES (per-user open cap) or RATE_LIMITED, 422 on a bad
    // kind / note / file_id, 404 when the item is out of reach.

    /** The caller's own reports on the item, newest first. */
    @GET("api/v1/items/{id}/issues")
    suspend fun getMyIssues(@Path("id") itemId: String): ApiResponse<List<MediaIssue>>

    @POST("api/v1/items/{id}/issues")
    suspend fun reportIssue(
        @Path("id") itemId: String,
        @Body body: CreateIssueBody,
    ): ApiResponse<MediaIssue>

    // ── Audiobook listening speed (v2.5) ────────────────────────────────────
    // internal/api/v1/audiobook.go. {id} is a book or one of its chapters
    // (the speed is the book's); 422 for anything else, 404 for an item out
    // of reach — and on a server that predates the route. The server also
    // keeps audiobook bookmarks; the TV client doesn't surface those.

    @GET("api/v1/items/{id}/playback-rate")
    suspend fun getPlaybackRate(@Path("id") itemId: String): ApiResponse<PlaybackRate>

    /** 204 No Content. Rate 0.5 – 3.0. */
    @PUT("api/v1/items/{id}/playback-rate")
    suspend fun setPlaybackRate(
        @Path("id") itemId: String,
        @Body body: PlaybackRateRequest,
    )

    // ── Transcode ───────────────────────────────────────────────────────────

    @POST("api/v1/items/{id}/transcode")
    suspend fun startTranscode(
        @Path("id") itemId: String,
        @Body body: TranscodeRequest,
    ): ApiResponse<TranscodeSession>

    @POST("api/v1/items/{id}/playback-decision")
    suspend fun playbackDecision(
        @Path("id") itemId: String,
        @Body body: PlaybackDecisionRequest,
    ): ApiResponse<PlaybackDecision>

    @DELETE("api/v1/transcode/sessions/{sid}")
    suspend fun stopTranscode(
        @Path("sid") sessionId: String,
        @Query("token") token: String,
    )

    // ── Search ──────────────────────────────────────────────────────────────

    @GET("api/v1/search")
    suspend fun search(
        @Query("q") query: String,
        @Query("limit") limit: Int = 30,
        @Query("library_id") libraryId: String? = null,
    ): ApiResponse<List<SearchResult>>

    // ── Favorites ───────────────────────────────────────────────────────────

    @GET("api/v1/favorites")
    suspend fun getFavorites(
        @Query("limit") limit: Int = 50,
        @Query("offset") offset: Int = 0,
    ): ApiListResponse<FavoriteItem>

    @POST("api/v1/items/{id}/favorite")
    suspend fun addFavorite(@Path("id") id: String)

    @DELETE("api/v1/items/{id}/favorite")
    suspend fun removeFavorite(@Path("id") id: String)

    // ── Collections ─────────────────────────────────────────────────────────

    @GET("api/v1/collections")
    suspend fun getCollections(): ApiResponse<List<MediaCollection>>

    @GET("api/v1/collections/{id}/items")
    suspend fun getCollectionItems(
        @Path("id") id: String,
        @Query("limit") limit: Int = 50,
        @Query("offset") offset: Int = 0,
    ): ApiListResponse<CollectionItem>

    // ── Preferences ─────────────────────────────────────────────────────────

    @GET("api/v1/users/me/preferences")
    suspend fun getPreferences(): ApiResponse<UserPreferences>

    // 204 No Content — declaring a body made Retrofit's await() reject the null
    // body with a KotlinNullPointerException, so every settings save reported a
    // raw exception in the UI even though the write had succeeded. Same shape as
    // logout / addFavorite / setListenBrainz above.
    @PUT("api/v1/users/me/preferences")
    suspend fun setPreferences(@Body body: UserPreferences)

    // ── Scrobbling (per-user ListenBrainz link, authenticated) ────────────────

    @GET("api/v1/users/me/scrobble")
    suspend fun scrobbleStatus(): ApiResponse<ScrobbleStatusResponse>

    /** Link/update the token (empty unlinks). 204 No Content — no body. */
    @PUT("api/v1/users/me/scrobble/listenbrainz")
    suspend fun setListenBrainz(@Body body: SetListenBrainzRequest)

    // ── Parental watch limits (self) ──────────────────────────────────────────

    /** The caller's own watch policy + today's usage + whether playback is
     *  allowed right now. The player pre-flights this before a stream starts;
     *  the transcode-start / progress 403 (PARENTAL_LIMIT) catches the rest. */
    @GET("api/v1/users/me/watch-limit")
    suspend fun getWatchLimit(): ApiResponse<WatchLimitData>

    // ── History ─────────────────────────────────────────────────────────────

    @GET("api/v1/history")
    suspend fun getHistory(
        @Query("limit") limit: Int = 50,
        @Query("offset") offset: Int = 0,
    ): ApiListResponse<HistoryItem>

    // ── Live TV ─────────────────────────────────────────────────────────────

    /** Lists configured + enabled channels. The disabled-channel
     *  curation happens via the web settings UI; the TV client
     *  always asks for enabled-only so the lineup matches what
     *  the user expects to see. */
    @GET("api/v1/tv/channels")
    suspend fun getChannels(@Query("enabled_only") enabledOnly: Boolean = true): ApiResponse<List<Channel>>

    /** Up to two rows per channel (current + next program). The
     *  client merges by channel_id against the channels list and
     *  shows "no guide data" for channels missing from the response. */
    @GET("api/v1/tv/channels/now-next")
    suspend fun getNowAndNext(): ApiResponse<List<NowNext>>

    /** Recordings for the calling user. status filter:
     *  "scheduled" | "recording" | "completed" | "failed" | "cancelled".
     *  Empty = all statuses. */
    @GET("api/v1/tv/recordings")
    suspend fun getRecordings(
        @Query("status") status: String? = null,
        @Query("limit") limit: Int = 100,
        @Query("offset") offset: Int = 0,
    ): ApiResponse<List<Recording>>

    // ── Cross-device playback transfer (v2.2) ──────────────────────────────
    // POST sends a "play on Living Room TV" handoff. The receiving TV
    // listens for the resulting `playback.transfer` SSE event in
    // NotificationsRepository.subscribePlaybackTransfers and matches
    // `target_client_name` against its own registration before acting.

    @POST("api/v1/playback/transfer")
    suspend fun transferPlayback(@Body body: PlaybackTransferRequest): Response<Unit>

    // ── External subtitles (OpenSubtitles) ──────────────────────────────────

    /** Search OpenSubtitles for additional subtitle tracks for an
     *  item. Server enriches the query with title / year / IMDB ID
     *  derived from the item itself; lang filters by ISO-639. */
    @GET("api/v1/items/{id}/subtitles/search")
    suspend fun searchOnlineSubtitles(
        @Path("id") itemId: String,
        @Query("lang") lang: String? = null,
        @Query("query") query: String? = null,
    ): ApiListResponse<OnlineSubtitle>

    /** Download one of the search results onto a specific file_id.
     *  Server fetches the .srt from OpenSubtitles, persists it next
     *  to the media file, and returns the new external_subtitle row
     *  that the next item-fetch surfaces in subtitle_streams. */
    @POST("api/v1/items/{id}/subtitles/download")
    suspend fun downloadOnlineSubtitle(
        @Path("id") itemId: String,
        @Body body: SubtitleDownloadRequest,
    ): Response<Unit>

    // ── Health ──────────────────────────────────────────────────────────────

    @GET("health/live")
    suspend fun healthCheck(): Response<Unit>
}
