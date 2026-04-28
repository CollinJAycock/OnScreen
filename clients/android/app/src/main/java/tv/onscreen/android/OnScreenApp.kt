package tv.onscreen.android

import android.app.Application
import dagger.hilt.android.HiltAndroidApp
import java.security.Security

@HiltAndroidApp
class OnScreenApp : Application() {

    override fun onCreate() {
        super.onCreate()
        disableStrictRevocationChecking()
    }

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
