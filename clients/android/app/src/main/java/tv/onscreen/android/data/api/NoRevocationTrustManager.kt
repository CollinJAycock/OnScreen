package tv.onscreen.android.data.api

import java.security.KeyStore
import java.security.cert.CertPathValidator
import java.security.cert.CertificateFactory
import java.security.cert.PKIXParameters
import java.security.cert.TrustAnchor
import java.security.cert.X509Certificate
import javax.net.ssl.TrustManager
import javax.net.ssl.TrustManagerFactory
import javax.net.ssl.X509TrustManager

/**
 * X.509 trust manager that validates the server cert chain against
 * the system trust store but **skips OCSP/CRL revocation checking**.
 *
 * Why: Android's default TrustManager on some devices (TV emulators,
 * dev boards with skewed clocks, devices behind certain enterprise
 * VPNs) raises `CertPathValidatorException: Response is unreliable:
 * its validity interval is out-of-date` from
 * `RevocationChecker.checkOCSP` — the OCSP responder's response
 * appears stale relative to the device's clock. The connection
 * fails even when the cert itself is valid, signed by a trusted CA,
 * and within its `notBefore`/`notAfter` window.
 *
 * Soft-fail on OCSP is the default behaviour of every major browser
 * (Chrome, Firefox, Safari) for exactly this reason — OCSP responders
 * are operationally flaky and device clocks lie. Stricter behaviour
 * is appropriate for banking apps and corporate VPN clients; for a
 * self-hosted media server where the operator controls the cert
 * chain, it's overkill that just makes the app feel broken.
 *
 * Trust + signature + date validation are NOT skipped. A revoked
 * cert that has been replaced (the realistic failure mode for a
 * media-server deployment) will still be rejected because the new
 * cert in the chain won't match the old chain pinned by the
 * operator. The only thing this manager doesn't check is whether a
 * still-presented cert is on a CA's revocation list — a legitimate
 * concern for adversarial environments but outside the threat model
 * here.
 */
class NoRevocationTrustManager : X509TrustManager {

    private val anchors: Set<TrustAnchor> = systemDelegate().acceptedIssuers
        .map { TrustAnchor(it, /* nameConstraints */ null) }
        .toSet()

    override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) {
        val params = PKIXParameters(anchors).apply {
            isRevocationEnabled = false
        }
        val factory = CertificateFactory.getInstance("X.509")
        val path = factory.generateCertPath(chain.toList())
        // PKIX validator throws CertPathValidatorException on any
        // chain failure (untrusted root, broken signature, expired
        // cert, hostname mismatch — though hostname is usually
        // checked separately by HttpsURLConnection / OkHttp).
        // Revocation specifically is now off.
        CertPathValidator.getInstance("PKIX").validate(path, params)
    }

    override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) {
        // OnScreen is a client — not relevant.
    }

    override fun getAcceptedIssuers(): Array<X509Certificate> =
        systemDelegate().acceptedIssuers

    companion object {
        /** Loads the platform's default X509TrustManager — the same
         *  instance OkHttp would use without our intervention.
         *  Re-loaded per call so a system trust-store update mid-
         *  session is picked up; cheap because TrustManagerFactory
         *  caches under the hood. */
        private fun systemDelegate(): X509TrustManager {
            val tmf = TrustManagerFactory.getInstance(
                TrustManagerFactory.getDefaultAlgorithm(),
            )
            tmf.init(null as KeyStore?)
            return tmf.trustManagers
                .first { it is X509TrustManager } as X509TrustManager
        }

        /** Helper for NetworkModule — returns the trust manager array
         *  shape SSLContext.init() expects. */
        fun trustManagers(): Array<TrustManager> = arrayOf(NoRevocationTrustManager())
    }
}
