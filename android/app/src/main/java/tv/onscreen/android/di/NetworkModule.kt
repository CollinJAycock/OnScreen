package tv.onscreen.android.di

import com.squareup.moshi.Moshi
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent
import okhttp3.OkHttpClient
import retrofit2.Retrofit
import retrofit2.converter.moshi.MoshiConverterFactory
import tv.onscreen.android.data.api.AuthInterceptor
import tv.onscreen.android.data.api.BaseUrlInterceptor
import tv.onscreen.android.data.api.OnScreenApi
import tv.onscreen.android.data.api.TokenAuthenticator
import tv.onscreen.android.data.prefs.ServerPrefs
import java.util.concurrent.TimeUnit
import javax.inject.Qualifier
import javax.inject.Singleton

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

    /** Plain client for auth refresh calls — no authenticator, avoids circular dep. */
    @Provides
    @Singleton
    @AuthClient
    fun provideAuthOkHttpClient(baseUrlInterceptor: BaseUrlInterceptor): OkHttpClient {
        return OkHttpClient.Builder()
            .connectTimeout(10, TimeUnit.SECONDS)
            .readTimeout(15, TimeUnit.SECONDS)
            .addInterceptor(baseUrlInterceptor)
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
        val builder = OkHttpClient.Builder()
            .connectTimeout(15, TimeUnit.SECONDS)
            .readTimeout(60, TimeUnit.SECONDS)
            .writeTimeout(30, TimeUnit.SECONDS)
            .addInterceptor(baseUrlInterceptor)
            .addInterceptor(AuthInterceptor(prefs))
            .authenticator(TokenAuthenticator(prefs) { authApi })

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
