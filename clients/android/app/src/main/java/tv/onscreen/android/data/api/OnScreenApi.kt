package tv.onscreen.android.data.api

import retrofit2.Response
import retrofit2.http.*
import tv.onscreen.android.data.model.*

interface OnScreenApi {

    // ── Auth ────────────────────────────────────────────────────────────────

    @POST("api/v1/auth/login")
    suspend fun login(@Body body: LoginRequest): ApiResponse<TokenPair>

    @POST("api/v1/auth/refresh")
    suspend fun refresh(@Body body: RefreshRequest): ApiResponse<TokenPair>

    @POST("api/v1/auth/logout")
    suspend fun logout(@Body body: LogoutRequest)

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
    ): ApiListResponse<MediaItem>

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

    // ── Transcode ───────────────────────────────────────────────────────────

    @POST("api/v1/items/{id}/transcode")
    suspend fun startTranscode(
        @Path("id") itemId: String,
        @Body body: TranscodeRequest,
    ): ApiResponse<TranscodeSession>

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

    // ── Discover (TMDB-backed) + Requests ───────────────────────────────────

    @GET("api/v1/discover/search")
    suspend fun discoverSearch(
        @Query("q") query: String,
        @Query("limit") limit: Int = 12,
    ): ApiResponse<List<DiscoverItem>>

    @POST("api/v1/requests")
    suspend fun createRequest(
        @Body body: CreateRequestBody,
    ): ApiResponse<MediaRequest>

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
