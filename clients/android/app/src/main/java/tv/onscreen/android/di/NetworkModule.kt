package tv.onscreen.android.di

import com.squareup.moshi.Moshi
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import okhttp3.OkHttpClient
import okhttp3.logging.HttpLoggingInterceptor
import retrofit2.Retrofit
import retrofit2.converter.moshi.MoshiConverterFactory
import tv.onscreen.android.data.api.AuthInterceptor
import tv.onscreen.android.data.api.BaseUrlInterceptor
import tv.onscreen.android.data.api.NoRevocationTrustManager
import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.api.TokenAuthenticator
import tv.onscreen.android.data.prefs.ServerPrefs
import java.util.concurrent.TimeUnit
import javax.inject.Qualifier
import javax.inject.Singleton
import javax.net.ssl.SSLContext

@Qualifier
@Retention(AnnotationRetention.BINARY)
annotation class AuthClient

@Module
@InstallIn(SingletonComponent::class)
object NetworkModule {

    @Provides
    @Singleton
    fun provideMoshi(): Moshi = Moshi.Builder().build()

    @Provides
    @Singleton
    fun provideBaseUrlInterceptor(prefs: ServerPrefs): BaseUrlInterceptor {
        return BaseUrlInterceptor(prefs)
    }

    /** OCSP/CRL soft-fail TLS factory shared by all OkHttp clients in
     *  the app. Builds an SSLContext around [NoRevocationTrustManager]
     *  so a stale OCSP response (the most common cause of "Chain
     *  validation failed" on devices with skewed clocks or behind
     *  certain enterprise networks) doesn't break the connection.
     *  See the trust-manager file for the threat-model rationale. */
    private data class Tls(
        val socketFactory: javax.net.ssl.SSLSocketFactory,
        val trustManager: javax.net.ssl.X509TrustManager,
    )

    private fun buildTls(): Tls {
        val tm = NoRevocationTrustManager()
        val ctx = SSLContext.getInstance("TLS").apply {
            init(null, arrayOf<javax.net.ssl.TrustManager>(tm), null)
        }
        return Tls(ctx.socketFactory, tm)
    }

    /** Logcat interceptor so we can see request paths, status codes,
     *  and rough timing of every API call. Headers + body are
     *  intentionally skipped to keep bearer tokens out of logs.
     *  Tighten to NONE before shipping a release build (BuildConfig.
     *  DEBUG branching needs `buildFeatures { buildConfig = true }`
     *  on AGP 9 — opt in when we add a release variant). */
    private fun loggingInterceptor(): HttpLoggingInterceptor =
        HttpLoggingInterceptor().apply {
            level = HttpLoggingInterceptor.Level.BASIC
        }

    /** Plain client for auth refresh calls — no authenticator, avoids circular dep. */
    @Provides
    @Singleton
    @AuthClient
    fun provideAuthOkHttpClient(baseUrlInterceptor: BaseUrlInterceptor): OkHttpClient {
        val tls = buildTls()
        return OkHttpClient.Builder()
            .connectTimeout(10, TimeUnit.SECONDS)
            .readTimeout(15, TimeUnit.SECONDS)
            .addInterceptor(baseUrlInterceptor)
            .addInterceptor(loggingInterceptor())
            .sslSocketFactory(tls.socketFactory, tls.trustManager)
            .build()
    }

    @Provides
    @Singleton
    @AuthClient
    fun provideAuthRetrofit(@AuthClient client: OkHttpClient, moshi: Moshi): Retrofit {
        return Retrofit.Builder()
            .baseUrl("http://localhost/")
            .client(client)
            .addConverterFactory(MoshiConverterFactory.create(moshi))
            .build()
    }

    @Provides
    @Singleton
    @AuthClient
    fun provideAuthApi(@AuthClient retrofit: Retrofit): OnScreenApi {
        return retrofit.create(OnScreenApi::class.java)
    }

    /** Main client with auth interceptor and token refresh authenticator. */
    @Provides
    @Singleton
    fun provideOkHttpClient(
        prefs: ServerPrefs,
        baseUrlInterceptor: BaseUrlInterceptor,
        @AuthClient authApi: OnScreenApi,
    ): OkHttpClient {
        val tls = buildTls()
        // OkHttp's default Dispatcher caps concurrent requests per
        // host at 5. The shared client is also Coil's HTTP backend,
        // so a fast-scrolling photo viewer or a library grid loading
        // 50 thumbnails saturates that limit and blocks new requests
        // until in-flight ones drain. Bump per-host to 16 — covers
        // a row of cards plus background pre-fetches without flooding
        // the server. Total max stays at the default 64.
        val dispatcher = okhttp3.Dispatcher().apply {
            maxRequestsPerHost = 16
        }
        val builder = OkHttpClient.Builder()
            .connectTimeout(15, TimeUnit.SECONDS)
            .readTimeout(60, TimeUnit.SECONDS)
            .writeTimeout(30, TimeUnit.SECONDS)
            .dispatcher(dispatcher)
            .addInterceptor(baseUrlInterceptor)
            .addInterceptor(AuthInterceptor(prefs))
            .addInterceptor(loggingInterceptor())
            .authenticator(TokenAuthenticator(prefs) { authApi })
            .sslSocketFactory(tls.socketFactory, tls.trustManager)

        return builder.build()
    }

    @Provides
    @Singleton
    fun provideRetrofit(client: OkHttpClient, moshi: Moshi): Retrofit {
        return Retrofit.Builder()
            .baseUrl("http://localhost/")
            .client(client)
            .addConverterFactory(MoshiConverterFactory.create(moshi))
            .build()
    }

    @Provides
    @Singleton
    fun provideOnScreenApi(retrofit: Retrofit): OnScreenApi {
        return retrofit.create(OnScreenApi::class.java)
    }
}
