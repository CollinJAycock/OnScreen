package tv.onscreen.mobile.di

import android.content.Context
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import tv.onscreen.mobile.data.prefs.PlaybackPrefs
import tv.onscreen.mobile.data.prefs.ServerPrefs
import tv.onscreen.mobile.i18n.AppLocales
import tv.onscreen.mobile.i18n.LanguageController
import javax.inject.Singleton

@Module
@InstallIn(SingletonComponent::class)
object AppModule {

    @Provides
    @Singleton
    fun provideServerPrefs(@ApplicationContext context: Context): ServerPrefs {
        return ServerPrefs(context)
    }

    @Provides
    @Singleton
    fun providePlaybackPrefs(@ApplicationContext context: Context): PlaybackPrefs {
        return PlaybackPrefs(context)
    }

    /** The app's interface language (see AppLocales). The same instance as
     *  AppLocales.controller, which MainActivity reads before injection. */
    @Provides
    @Singleton
    fun provideLanguageController(@ApplicationContext context: Context): LanguageController =
        AppLocales.controller(context)
}
