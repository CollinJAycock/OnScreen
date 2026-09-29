package tv.onscreen.mobile.data.api

import retrofit2.Response
import retrofit2.http.*
import tv.onscreen.mobile.data.model.*

interface OnScreenApi {

    // ── Auth ────────────────────────────────────────────────────────────────

    @POST("api/v1/auth/login")
    suspend fun login(@Body body: LoginRequest): ApiResponse<TokenPair>

    /** Second step of a two-factor login — challenge token + code
     *  (or recovery code) in exchange for a real token pair. */
    @POST("api/v1/auth/totp/verify")
    suspend fun verifyTotp(@Body body: TotpVerifyRequest): ApiResponse<TokenPair>

    // ── 2FA self-management (authenticated) ───────────────────────────────────
    @GET("api/v1/auth/totp/status")
    suspend fun totpStatus(): ApiResponse<TotpStatusResponse>

    @POST("api/v1/auth/totp/setup")
    suspend fun totpSetup(): ApiResponse<TotpSetupResponse>

    @POST("api/v1/auth/totp/activate")
    suspend fun totpActivate(@Body body: TotpCodeRequest): ApiResponse<TotpActivateResponse>

    @POST("api/v1/auth/totp/disable")
    suspend fun totpDisable(@Body body: TotpCodeRequest): ApiResponse<TotpStatusResponse>

    @POST("api/v1/auth/refresh")
    suspend fun refresh(@Body body: RefreshRequest): ApiResponse<TokenPair>

    /** Revoke a refresh token. The bearer is passed explicitly because
     *  sign-out clears local auth BEFORE this call (so a dead server can't
     *  strand the user signed in) — AuthInterceptor would then attach
     *  nothing, and the server's logout route runs under Optional auth:
     *  without valid claims it skips revocation and still answers 204, so
     *  the session would survive a "successful" sign-out. */
    @POST("api/v1/auth/logout")
    suspend fun logout(
        @Body body: LogoutRequest,
        @Header("Authorization") authorization: String? = null,
    )

    /** [refresh] against an explicit absolute URL. Used by sign-out, which
     *  runs detached AFTER local auth is cleared: routing through the
     *  placeholder base (BaseUrlInterceptor reads the server URL at request
     *  time) could deliver the OLD server's refresh token to a server the
     *  user entered in the meantime. Absolute URLs bypass that rewrite. */
    @POST
    suspend fun refreshAt(@Url url: String, @Body body: RefreshRequest): ApiResponse<TokenPair>

    /** [logout] against an explicit absolute URL — see [refreshAt]. */
    @POST
    suspend fun logoutAt(
        @Url url: String,
        @Body body: LogoutRequest,
        @Header("Authorization") authorization: String? = null,
    )

    // ── Federated auth discovery ────────────────────────────────────────────

    /** Per-provider enabled flag + display name. Server emits these
     *  individually rather than a combined endpoint; client fans out
     *  three calls in parallel. */
    @GET("api/v1/auth/oidc/enabled")
    suspend fun getOidcEnabled(): ApiResponse<AuthProviderStatus>

    @GET("api/v1/auth/saml/enabled")
    suspend fun getSamlEnabled(): ApiResponse<AuthProviderStatus>

    @GET("api/v1/auth/ldap/enabled")
    suspend fun getLdapEnabled(): ApiResponse<AuthProviderStatus>

    /** LDAP directly accepts a username/password from the phone — no
     *  browser handoff needed. The server binds to the LDAP server
     *  with these credentials, JIT-provisions the user on first
     *  success, and returns the same TokenPair shape as local login. */
    @POST("api/v1/auth/ldap/login")
    suspend fun loginLdap(@Body body: LoginRequest): ApiResponse<TokenPair>

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
    ): Response<ApiResponse<TokenPair>>

    // ── Hub ─────────────────────────────────────────────────────────────────

    @GET("api/v1/hub")
    suspend fun getHub(): ApiResponse<HubData>

    // ── Libraries ───────────────────────────────────────────────────────────

    @GET("api/v1/libraries")
    suspend fun getLibraries(): ApiResponse<List<Library>>

    @GET("api/v1/libraries/{id}/genres")
    suspend fun getLibraryGenres(@Path("id") libraryId: String): ApiResponse<List<String>>

    @GET("api/v1/libraries/{id}/items")
    suspend fun getLibraryItems(
        @Path("id") libraryId: String,
        @Query("limit") limit: Int = 50,
        @Query("offset") offset: Int = 0,
        @Query("sort") sort: String? = null,
        @Query("sort_dir") sortDir: String? = null,
        @Query("genre") genre: String? = null,
        // v2.5: unwatched | in_progress | watched (null = all). Older
        // servers ignore the unknown param and return everything.
        @Query("watch") watch: String? = null,
    ): ApiListResponse<MediaItem>

    /** "Surprise me": one random item the caller can see, under the same
     *  filters as the listing. 404 when nothing matches, 501 when the
     *  server's watch store isn't wired. */
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

    @GET("api/v1/items/{id}/trickplay")
    suspend fun getTrickplayStatus(@Path("id") id: String): ApiResponse<TrickplayStatus>

    @GET("api/v1/items/{id}/lyrics")
    suspend fun getLyrics(@Path("id") id: String): ApiResponse<LyricsResponse>

    // ── Photos: EXIF / timeline / map ───────────────────────────────────────

    @GET("api/v1/items/{id}/exif")
    suspend fun getPhotoExif(@Path("id") id: String): ApiResponse<PhotoExif>

    @GET("api/v1/photos/timeline")
    suspend fun getPhotoTimeline(
        @Query("library_id") libraryId: String,
    ): ApiListResponse<PhotoTimelineBucket>

    @GET("api/v1/photos/map")
    suspend fun getPhotoMap(
        @Query("library_id") libraryId: String,
        @Query("limit") limit: Int = 1000,
    ): ApiListResponse<PhotoMapPoint>

    // ── Watching-status mirror (anime track, but works on every type) ───────

    // NOTE: bare WatchStatusResponse, NOT the ApiResponse<…> envelope. The
    // watch-status handler (internal/api/v1/watch_status.go) replies via
    // respond.JSON — a plain `{status,created_at,updated_at}` body — while
    // every other endpoint uses respond.Success (`{data:{…}}`). Declaring
    // these as ApiResponse<…> made Moshi throw on the missing `data` key, so
    // both read and write silently failed. Keep these un-enveloped unless the
    // server switches this route to respond.Success.
    @GET("api/v1/items/{id}/watch-status")
    suspend fun getWatchStatus(@Path("id") id: String): WatchStatusResponse

    @PUT("api/v1/items/{id}/watch-status")
    suspend fun setWatchStatus(
        @Path("id") id: String,
        @Body body: WatchStatusRequest,
    ): WatchStatusResponse

    @DELETE("api/v1/items/{id}/watch-status")
    suspend fun clearWatchStatus(@Path("id") id: String)

    // ── Manual watch state (v2.5) ────────────────────────────────────────────
    // Marks + dismiss answer 204 (no body). A show / season mark expands to
    // every episode under it server-side; other non-video types answer 422.
    // The three writes share a tight per-user rate limit (→ 429).

    @POST("api/v1/items/{id}/watched")
    suspend fun markWatched(@Path("id") id: String)

    @DELETE("api/v1/items/{id}/watched")
    suspend fun markUnwatched(@Path("id") id: String)

    /** Hide from every Continue Watching row (and Next Up) until the user
     *  next records activity on it. 404 when there was nothing to hide. */
    @POST("api/v1/items/{id}/dismiss-continue-watching")
    suspend fun dismissContinueWatching(@Path("id") id: String)

    /** Which episode Play on a show / season starts (other types: 422). */
    @GET("api/v1/items/{id}/up-next")
    suspend fun getUpNext(@Path("id") id: String): ApiResponse<UpNext>

    // ── Report a problem (v2.5) ──────────────────────────────────────────────
    // internal/api/v1/issues.go. Both answer 404 for an item the caller
    // can't see (library ACL + rating ceiling).

    /** The caller's own reports on the item, newest first. */
    @GET("api/v1/items/{id}/issues")
    suspend fun listMyIssues(@Path("id") id: String): ApiResponse<List<MediaIssue>>

    /** 201 with the report. 409 ALREADY_REPORTED for a second open report of
     *  the same kind; 429 TOO_MANY_OPEN_ISSUES past the per-user cap, or
     *  RATE_LIMITED from the route limiter; 422 VALIDATION for a bad kind /
     *  note / file_id. */
    @POST("api/v1/items/{id}/issues")
    suspend fun reportIssue(
        @Path("id") id: String,
        @Body body: CreateIssueRequest,
    ): ApiResponse<MediaIssue>

    // ── Audiobooks: listening speed + bookmarks (v2.5) ───────────────────────
    // internal/api/v1/audiobook.go. {id} on the item routes may be a book or
    // one of its chapters; 422 for anything else, 404 for an item the caller
    // can't see — and on a server that predates the routes.

    @GET("api/v1/items/{id}/playback-rate")
    suspend fun getPlaybackRate(@Path("id") id: String): ApiResponse<PlaybackRate>

    /** 204 No Content. */
    @PUT("api/v1/items/{id}/playback-rate")
    suspend fun setPlaybackRate(
        @Path("id") id: String,
        @Body body: PlaybackRateRequest,
    )

    /** Every bookmark the caller has in the book, in listening order. */
    @GET("api/v1/items/{id}/bookmarks")
    suspend fun listBookmarks(@Path("id") id: String): ApiResponse<List<Bookmark>>

    /** {id} is the PLAYABLE item (a single-file book or the chapter being
     *  played). 201 with the bookmark; 409 BOOKMARK_LIMIT past 1000 in one
     *  book; 400 for a note over 500 characters. */
    @POST("api/v1/items/{id}/bookmarks")
    suspend fun createBookmark(
        @Path("id") id: String,
        @Body body: CreateBookmarkRequest,
    ): ApiResponse<Bookmark>

    /** 204 No Content. */
    @PATCH("api/v1/bookmarks/{id}")
    suspend fun updateBookmark(
        @Path("id") id: String,
        @Body body: BookmarkNoteRequest,
    )

    /** 204 No Content. */
    @DELETE("api/v1/bookmarks/{id}")
    suspend fun deleteBookmark(@Path("id") id: String)

    // ── Online subtitles (OpenSubtitles proxy) ──────────────────────────────

    @GET("api/v1/items/{id}/subtitles/search")
    suspend fun searchOnlineSubtitles(
        @Path("id") id: String,
        @Query("lang") lang: String? = null,
        @Query("query") query: String? = null,
    ): ApiListResponse<OnlineSubtitle>

    @POST("api/v1/items/{id}/subtitles/download")
    suspend fun downloadOnlineSubtitle(
        @Path("id") id: String,
        @Body body: SubtitleDownloadRequest,
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

    // ── Playlists (incl. smart) ─────────────────────────────────────────────

    @GET("api/v1/playlists")
    suspend fun listPlaylists(): ApiListResponse<Playlist>

    @POST("api/v1/playlists")
    suspend fun createPlaylist(@Body body: CreatePlaylistRequest): ApiResponse<Playlist>

    @DELETE("api/v1/playlists/{id}")
    suspend fun deletePlaylist(@Path("id") id: String)

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

    @PUT("api/v1/users/me/preferences")
    suspend fun setPreferences(@Body body: UserPreferences): ApiResponse<UserPreferences>

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

    // ── Health ──────────────────────────────────────────────────────────────

    @GET("health/live")
    suspend fun healthCheck(): Response<Unit>
}
