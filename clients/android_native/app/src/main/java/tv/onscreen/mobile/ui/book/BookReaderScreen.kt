package tv.onscreen.mobile.ui.book

import android.annotation.SuppressLint
import android.content.Context
import android.util.Log
import android.webkit.ConsoleMessage
import android.webkit.JavascriptInterface
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.VerticalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.unit.LayoutDirection
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.webkit.WebViewAssetLoader
import androidx.webkit.WebViewAssetLoader.AssetsPathHandler
import coil.compose.AsyncImage
import dagger.hilt.android.lifecycle.HiltViewModel
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import okhttp3.Request
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.data.repository.ItemRepository
import java.io.File
import javax.inject.Inject

/**
 * Reader for book items — CBZ / CBR pages render through Coil from
 * /api/v1/items/{id}/book/page/{n}; EPUB renders inside a WebView
 * hosting a bundled epub.js. Mirrors the web client's reader so the
 * two stay at parity; the server already serves both shapes, only
 * the rendering changes.
 *
 * Pagination state lives in [BookReaderUi.currentPage]:
 *   - CBZ/CBR → 1-indexed image entry in the archive.
 *   - EPUB    → 1-indexed spine entry; intra-chapter page-flips
 *               don't move this counter (they move the viewport
 *               inside the rendition only — matches epub.js's
 *               'relocated' semantics).
 *
 * [BookReaderUi.readingDirection] comes from the AniList-enriched
 * manga track ('rtl' / 'ttb') and falls back to 'ltr' for ordinary
 * books. EPUB ignores the direction — epub.js owns its own flow.
 */
@HiltViewModel
class BookReaderViewModel @Inject constructor(
    @ApplicationContext private val appContext: Context,
    private val itemRepo: ItemRepository,
    private val serverPrefs: ServerPrefs,
    private val okHttpClient: OkHttpClient,
) : ViewModel() {

    private val _state = MutableStateFlow(BookReaderUi(loading = true))
    val state: StateFlow<BookReaderUi> = _state.asStateFlow()

    fun load(itemId: String) {
        viewModelScope.launch {
            _state.value = BookReaderUi(loading = true)
            try {
                val detail = itemRepo.getItem(itemId)
                if (detail.type != "book") {
                    _state.value = BookReaderUi(loading = false, error = "Not a book.")
                    return@launch
                }
                val serverUrl = serverPrefs.getServerUrl()?.trimEnd('/').orEmpty()
                val container = (detail.files.firstOrNull()?.container ?: "cbz").lowercase()
                val format = when (container) {
                    "epub" -> BookFormat.EPUB
                    "cbr" -> BookFormat.CBR
                    else -> BookFormat.CBZ
                }
                // Page count: scanner stuffs the image-count (CBZ/CBR)
                // or spine length (EPUB) into duration_ms. See
                // migration 00059 for the rename rationale — we keep
                // the wire field name to avoid a schema churn for a
                // single book-only counter.
                val pageCount = (detail.duration_ms ?: 1L).toInt().coerceAtLeast(1)
                val direction = when (detail.reading_direction) {
                    "rtl", "ttb" -> detail.reading_direction!!
                    else -> "ltr"
                }
                val epubFile: File? = if (format == BookFormat.EPUB) {
                    val fileId = detail.files.firstOrNull()?.id
                    if (fileId == null) {
                        _state.value = BookReaderUi(
                            loading = false,
                            error = "EPUB has no file.",
                        )
                        return@launch
                    }
                    prefetchEpub(fileId, itemId, serverUrl)
                } else {
                    null
                }
                _state.value = BookReaderUi(
                    loading = false,
                    format = format,
                    serverUrl = serverUrl,
                    itemId = itemId,
                    pageCount = pageCount,
                    currentPage = 1,
                    readingDirection = direction,
                    epubFile = epubFile,
                    title = detail.title,
                )
            } catch (e: Exception) {
                _state.value = BookReaderUi(loading = false, error = e.message)
            }
        }
    }

    fun setPage(n: Int) {
        val s = _state.value
        if (s.pageCount <= 0) return
        _state.value = s.copy(currentPage = n.coerceIn(1, s.pageCount))
    }

    /** Pulls the .epub bytes through the shared OkHttp client so the
     *  AuthInterceptor adds Bearer, then writes to a per-item cache
     *  file. Returning a real on-disk path lets the WebViewAssetLoader
     *  serve it back to epub.js via fetch() without re-authenticating
     *  inside the WebView. */
    private suspend fun prefetchEpub(
        fileId: String,
        itemId: String,
        serverUrl: String,
    ): File = withContext(Dispatchers.IO) {
        val dir = File(appContext.cacheDir, "book-epubs").apply { mkdirs() }
        val out = File(dir, "$itemId.epub")
        if (out.exists() && out.length() > 0) return@withContext out
        val req = Request.Builder()
            // /media/stream/{fileId} is the same endpoint the player
            // hits — server's asset-route middleware accepts the
            // Bearer header even though it's also gated by a per-
            // session stream_token query param for video. For book
            // files the per-session token isn't issued, so the
            // header path is the one that works.
            .url("$serverUrl/media/stream/$fileId")
            .build()
        okHttpClient.newCall(req).execute().use { resp ->
            if (!resp.isSuccessful) {
                throw IllegalStateException("EPUB fetch failed: ${resp.code}")
            }
            val body = resp.body ?: throw IllegalStateException("EPUB body empty")
            out.outputStream().use { os -> body.byteStream().copyTo(os) }
        }
        out
    }
}

enum class BookFormat { CBZ, CBR, EPUB }

data class BookReaderUi(
    val loading: Boolean = false,
    val error: String? = null,
    val format: BookFormat = BookFormat.CBZ,
    val serverUrl: String = "",
    val itemId: String = "",
    val title: String = "",
    val pageCount: Int = 1,
    val currentPage: Int = 1,
    /** 'ltr', 'rtl', or 'ttb'. CBZ/CBR honour all three; EPUB
     *  ignores it (epub.js owns the flow). */
    val readingDirection: String = "ltr",
    /** Pre-fetched .epub path; only populated for EPUB. */
    val epubFile: File? = null,
)

@Composable
fun BookReaderScreen(
    itemId: String,
    onBack: () -> Unit,
    vm: BookReaderViewModel = hiltViewModel(),
) {
    LaunchedEffect(itemId) { vm.load(itemId) }
    val ui by vm.state.collectAsState()

    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(Color.Black),
    ) {
        when {
            ui.loading -> CircularProgressIndicator(Modifier.align(Alignment.Center))
            ui.error != null -> Text(
                ui.error!!,
                color = Color.White,
                modifier = Modifier.align(Alignment.Center).padding(24.dp),
            )
            ui.format == BookFormat.EPUB -> EpubReader(ui)
            else -> ImagePagedReader(ui, onPageChanged = { vm.setPage(it) })
        }

        // Back button overlay. Always visible — books don't have an
        // immersive-chrome story like the photo viewer yet; the user
        // needs a discoverable way out.
        IconButton(
            onClick = onBack,
            modifier = Modifier.align(Alignment.TopStart).padding(8.dp),
        ) {
            Icon(
                Icons.AutoMirrored.Filled.ArrowBack,
                contentDescription = "Back",
                tint = Color.White,
            )
        }

        // Page counter (bottom-centre). Hidden when the book is a
        // single page — would just be noise.
        if (!ui.loading && ui.error == null && ui.pageCount > 1) {
            Text(
                text = "${ui.currentPage} / ${ui.pageCount}",
                color = Color.White,
                style = MaterialTheme.typography.bodyMedium,
                modifier = Modifier
                    .align(Alignment.BottomCenter)
                    .padding(16.dp),
            )
        }
    }
}

/** CBZ/CBR page-per-image reader. Direction picks the pager axis:
 *  ltr/rtl use HorizontalPager (RTL is a LayoutDirection flip so the
 *  swipe + Compose's pre-loading both reverse together); ttb uses
 *  VerticalPager for webtoons. */
@Composable
private fun ImagePagedReader(
    ui: BookReaderUi,
    onPageChanged: (Int) -> Unit,
) {
    val pager = rememberPagerState(
        initialPage = (ui.currentPage - 1).coerceAtLeast(0),
    ) { ui.pageCount }

    LaunchedEffect(pager.currentPage) {
        onPageChanged(pager.currentPage + 1)
    }

    val page: @Composable (Int) -> Unit = { page ->
        // Server endpoint is 1-indexed.
        val url = "${ui.serverUrl}/api/v1/items/${ui.itemId}/book/page/${page + 1}"
        AsyncImage(
            model = url,
            contentDescription = null,
            contentScale = ContentScale.Fit,
            modifier = Modifier.fillMaxSize(),
        )
    }

    when (ui.readingDirection) {
        "ttb" -> VerticalPager(
            state = pager,
            modifier = Modifier.fillMaxSize(),
        ) { i -> page(i) }
        "rtl" -> CompositionLocalProvider(LocalLayoutDirection provides LayoutDirection.Rtl) {
            HorizontalPager(
                state = pager,
                modifier = Modifier.fillMaxSize(),
            ) { i -> page(i) }
        }
        else -> HorizontalPager(
            state = pager,
            modifier = Modifier.fillMaxSize(),
        ) { i -> page(i) }
    }
}

/** WebView host for EPUB. Loads our bundled epub.js + jszip from
 *  assets and the pre-fetched .epub from internal storage, all
 *  through one https://appassets.androidplatform.net origin so the
 *  iframe sandbox cooperates. */
@SuppressLint("SetJavaScriptEnabled")
@Composable
private fun EpubReader(ui: BookReaderUi) {
    val context = LocalContext.current
    val epubFile = ui.epubFile
    if (epubFile == null || !epubFile.exists()) {
        Text(
            "EPUB not available.",
            color = Color.White,
            modifier = Modifier.fillMaxSize(),
        )
        return
    }

    val assetLoader = remember(epubFile) {
        WebViewAssetLoader.Builder()
            .setDomain("appassets.androidplatform.net")
            .addPathHandler("/assets/", AssetsPathHandler(context))
            // Serve the pre-fetched .epub at /epub/file.epub. We hand
            // the path handler the parent dir of THIS book's file so
            // a leaked path traversal can't reach other items' caches
            // (or anywhere else in the app's storage).
            .addPathHandler(
                "/epub/",
                EpubFilePathHandler(epubFile),
            )
            .build()
    }

    AndroidView(
        // White matches the host HTML — keeps the user from seeing
        // a black flash between AndroidView attach and rendition
        // paint. The Box wrapper in BookReaderScreen still paints
        // black underneath so CBZ/CBR pillars look right; EPUB just
        // covers it.
        modifier = Modifier.fillMaxSize().background(Color.White),
        factory = { ctx ->
            // Enables `chrome://inspect` connections from a desktop
            // Chrome when the phone is USB-attached. Cheaper than
            // re-deploying with println scattered through viewer.js.
            // Gated on BuildConfig.DEBUG: `setWebContentsDebuggingEnabled(true)`
            // is process-global, so leaving it on in release lets any
            // attacker with USB debugging access (or a malicious USB
            // host) inspect the WebView's DOM + cookies on a victim's
            // device — including the per-session epub blob URLs. Off
            // in release builds.
            if (tv.onscreen.mobile.BuildConfig.DEBUG) {
                WebView.setWebContentsDebuggingEnabled(true)
            }
            WebView(ctx).apply {
                settings.javaScriptEnabled = true
                settings.domStorageEnabled = true
                // The .epub bundles internal stylesheets / fonts /
                // images; epub.js resolves them through blob: URLs
                // inside the rendition iframe. Without DOM storage
                // and JS, the rendition silently falls back to a
                // blank chapter.
                settings.allowFileAccess = false
                settings.allowContentAccess = false
                addJavascriptInterface(EpubBridge(), "AndroidBridge")
                webChromeClient = object : WebChromeClient() {
                    override fun onConsoleMessage(msg: ConsoleMessage): Boolean {
                        Log.d(
                            "EpubReader",
                            "${msg.messageLevel()} ${msg.sourceId()}:${msg.lineNumber()} ${msg.message()}",
                        )
                        return true
                    }
                }
                webViewClient = object : WebViewClient() {
                    override fun shouldInterceptRequest(
                        view: WebView,
                        request: WebResourceRequest,
                    ): WebResourceResponse? {
                        val resp = assetLoader.shouldInterceptRequest(request.url)
                        Log.d(
                            "EpubReader",
                            "intercept ${request.url} -> ${if (resp == null) "passthrough" else "handled mime=${resp.mimeType}"}",
                        )
                        return resp
                    }
                }
                loadUrl(
                    "https://appassets.androidplatform.net/assets/epub-viewer/index.html" +
                        "?p=${ui.currentPage}",
                )
            }
        },
    )
}

/** Stub bridge — epub.js posts page-change events here. We don't
 *  surface EPUB spine progress in the bottom counter yet (the
 *  rendition reports it post-relocate, and the page-counter overlay
 *  isn't visible inside the WebView area today). */
private class EpubBridge {
    @JavascriptInterface
    fun onPageChange(payload: String) { /* future: server progress hook */ }

    @JavascriptInterface
    fun onError(message: String) { /* future: surface to host UI */ }

    @JavascriptInterface
    fun onReady(unused: String) { /* future: hide loading overlay */ }

    @JavascriptInterface
    fun onExternalLink(href: String) { /* future: open in custom tab */ }
}

/** Serves the pre-fetched .epub at the registered path. The
 *  WebViewAssetLoader.InternalStoragePathHandler bundled in
 *  androidx.webkit only accepts a directory; we want to lock to one
 *  file per book so we roll a minimal handler instead.
 *
 *  Returns the full HTTP-style response (status + headers) rather
 *  than the bare InputStream constructor so Chromium's fetch()
 *  populates Response.headers and resolves the body length up
 *  front — some epub.js paths read Content-Length before pulling
 *  the array buffer. */
private class EpubFilePathHandler(
    private val file: File,
) : WebViewAssetLoader.PathHandler {
    override fun handle(path: String): WebResourceResponse? {
        if (!file.exists() || file.length() <= 0) {
            Log.w("EpubReader", "epub file missing: ${file.absolutePath}")
            return null
        }
        Log.d("EpubReader", "serving epub: ${file.absolutePath} (${file.length()} bytes)")
        val headers = mapOf(
            "Content-Length" to file.length().toString(),
            "Cache-Control" to "no-store",
        )
        return WebResourceResponse(
            "application/epub+zip",
            null,
            200,
            "OK",
            headers,
            file.inputStream(),
        )
    }
}
