package tv.onscreen.mobile.data.api

import java.security.KeyStore
import java.security.cert.X509Certificate
import javax.net.ssl.TrustManager
import javax.net.ssl.TrustManagerFactory
import javax.net.ssl.X509TrustManager

/**
 * X.509 trust manager that delegates fully to the platform's default
 * X509TrustManager — chain building (including AIA intermediate fetching for
 * servers that send an incomplete chain, e.g. Cloudflare edges), signature,
 * validity window, and the platform's default revocation policy (OCSP-
 * stapling-aware, soft-fail on a missing response — the posture every major
 * browser takes).
 *
 * The name is historical. An earlier version hand-rolled PKIX validation with
 * `isRevocationEnabled = false` to dodge `CertPathValidatorException: Response
 * is unreliable` errors on devices with skewed clocks / flaky OCSP responders.
 * That hand-rolled path also skipped AIA fetching, so it threw "Path does not
 * chain with any trust anchors" against public-CA servers that don't ship the
 * full chain in the handshake (hit a real user on a Samsung S24 FE pointed at a
 * Cloudflare-fronted box). Delegating to the system manager fixes both and
 * stops us maintaining error-prone custom certificate validation.
 *
 * This is NOT a trust-all manager: trust, signature, and validity are fully
 * enforced, and hostname verification is unaffected (OkHttp's default verifier
 * still runs). Matches the TV client (android).
 */
class NoRevocationTrustManager : X509TrustManager {

    // Delegate to the platform's full X509TrustManager for chain validation.
    private val delegate: X509TrustManager = systemDelegate()

    override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) {
        delegate.checkServerTrusted(chain, authType)
    }

    override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) {
        // OnScreen is a client — not relevant.
    }

    override fun getAcceptedIssuers(): Array<X509Certificate> = delegate.acceptedIssuers

    companion object {
        /** Loads the platform's default X509TrustManager — the same instance
         *  OkHttp would use without our intervention. */
        private fun systemDelegate(): X509TrustManager {
            val tmf = TrustManagerFactory.getInstance(
                TrustManagerFactory.getDefaultAlgorithm(),
            )
            tmf.init(null as KeyStore?)
            return tmf.trustManagers
                .first { it is X509TrustManager } as X509TrustManager
        }

        /** Helper for NetworkModule — returns the trust manager array shape
         *  SSLContext.init() expects. */
        fun trustManagers(): Array<TrustManager> = arrayOf(NoRevocationTrustManager())
    }
}
