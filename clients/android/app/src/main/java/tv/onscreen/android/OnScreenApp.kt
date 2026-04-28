package tv.onscreen.android

import android.app.Application
import coil.ImageLoader
import coil.ImageLoaderFactory
import dagger.hilt.android.HiltAndroidApp
import okhttp3.OkHttpClient
import java.security.Security
import javax.inject.Inject

/**
 * Application class. Wires:
 *   - JVM-level OCSP soft-fail (see disableStrictRevocationChecking
 *     below) — must run before any HTTPS happens.
 *   - Coil's singleton ImageLoader is configured against our
 *     Hilt-managed OkHttpClient so every imageView.load(url) call
 *     in the UI carries the Authorization: Bearer header. Without
 *     this, Coil uses its own bare OkHttpClient and artwork
 *     requests come back 401 (the server's /artwork/ route is
 *     RequiredAllowQueryToken — accepts header OR ?token= but
 *     not nothing). Symptom: home + detail screens render with
 *     placeholder-coloured boxes where posters should be while
 *     direct-play / transcode video still works fine because
 *     transcode session URLs carry their own per-session token.
 */
@HiltAndroidApp
class OnScreenApp : Application(), ImageLoaderFactory {

    /**
     * Hilt populates this after super.onCreate(); newImageLoader()
     * is invoked lazily on the first image load (well after the
     * Hilt graph is ready), so the lateinit isn't a hazard here.
     */
    @Inject lateinit var okHttpClient: OkHttpClient

    override fun onCreate() {
        super.onCreate()
        disableStrictRevocationChecking()
    }

    override fun newImageLoader(): ImageLoader =
        ImageLoader.Builder(this)
            .okHttpClient(okHttpClient)
            .crossfade(true)
            .build()

    /**
     * Disable OCSP/CRL revocation checking at the JVM PKIX layer
     * before any networking happens.
     *
     * Background: Conscrypt's TrustManagerImpl (Android's default
     * SSL TrustManager, used by every HTTPS path including OkHttp's
     * regardless of what custom TrustManager we install on the
     * SSLContext — the platform's RootTrustManager from
     * NetworkSecurityConfig wraps and pre-empts ours) hands the
     * cert chain to `sun.security.provider.certpath.PKIXValidator`.
     * That validator's `RevocationChecker` performs OCSP lookups
     * and rejects responses whose `thisUpdate`/`nextUpdate` window
     * doesn't include the device's current time. The failure
     * surfaces as `CertPathValidatorException: Response is
     * unreliable: its validity interval is out-of-date` — which
     * blows up TLS handshakes on devices with clock skew (common
     * on TV emulators / freshly-flashed boards) or when the OCSP
     * responder is operationally degraded.
     *
     * Setting `ocsp.enable=false` and
     * `com.sun.net.ssl.checkRevocation=false` is Sun's documented
     * way to disable revocation enforcement on the PKIX validator.
     * Cert trust + signature + dates are still checked — only the
     * OCSP/CRL freshness step is skipped. Same behaviour as
     * Chrome/Firefox/Safari soft-fail.
     *
     * Must run before any HTTPS request — the PKIX validator reads
     * these properties statically when it first initialises, so a
     * later flip has no effect. Application.onCreate is the
     * earliest hook the Hilt + Dagger graph fires.
     */
    private fun disableStrictRevocationChecking() {
        Security.setProperty("ocsp.enable", "false")
        System.setProperty("com.sun.net.ssl.checkRevocation", "false")
        System.setProperty("com.sun.security.enableCRLDP", "false")
    }
}
