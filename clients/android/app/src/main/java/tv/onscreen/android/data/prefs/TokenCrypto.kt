package tv.onscreen.android.data.prefs

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * Encrypts token strings with AES-256-GCM under a key held in the hardware-backed
 * AndroidKeyStore. The key is non-exportable and never leaves the Keystore, so a
 * rooted or forensically-imaged device can read the ciphertext on disk but cannot
 * recover the plaintext tokens without the device's hardware key — closing the
 * one residual exposure of plaintext-at-rest token storage.
 *
 * Wire format: `enc:v1:` + Base64(IV[12] || ciphertext+tag). A stored value
 * WITHOUT that prefix is treated as legacy plaintext (written before encryption
 * was added) and returned as-is; it is re-encrypted on the next write. That makes
 * the migration transparent — no existing user is logged out on upgrade.
 *
 * Failure posture is deliberately asymmetric:
 *  - encrypt() falls back to returning the plaintext if the crypto op fails, so a
 *    flaky Keystore can never LOSE a usable token (worst case = no worse than the
 *    old plaintext behaviour).
 *  - decrypt() of an enc: value that fails returns null, forcing a token refresh /
 *    re-login rather than handing back garbage.
 */
class TokenCrypto {
    private companion object {
        const val KEYSTORE = "AndroidKeyStore"
        const val KEY_ALIAS = "onscreen_token_key"
        const val PREFIX = "enc:v1:"
        const val IV_LEN = 12
        const val TAG_BITS = 128
        const val TRANSFORM = "AES/GCM/NoPadding"
    }

    @Volatile
    private var cachedKey: SecretKey? = null

    private fun key(): SecretKey {
        cachedKey?.let { return it }
        synchronized(this) {
            cachedKey?.let { return it }
            val ks = KeyStore.getInstance(KEYSTORE).apply { load(null) }
            val existing = (ks.getEntry(KEY_ALIAS, null) as? KeyStore.SecretKeyEntry)?.secretKey
            val k = existing ?: generateKey()
            cachedKey = k
            return k
        }
    }

    private fun generateKey(): SecretKey {
        val gen = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, KEYSTORE)
        gen.init(
            KeyGenParameterSpec.Builder(
                KEY_ALIAS,
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
            )
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                // No user-auth requirement: the media session / token-refresh path
                // must read tokens without a prompt, and a lock-screen change must
                // not invalidate them.
                .build(),
        )
        return gen.generateKey()
    }

    fun encrypt(plain: String): String =
        try {
            val cipher = Cipher.getInstance(TRANSFORM)
            cipher.init(Cipher.ENCRYPT_MODE, key())
            val iv = cipher.iv // 12-byte GCM nonce chosen by the provider
            val ct = cipher.doFinal(plain.toByteArray(Charsets.UTF_8))
            PREFIX + Base64.encodeToString(iv + ct, Base64.NO_WRAP)
        } catch (_: Exception) {
            plain // never lose a usable token to a crypto failure
        }

    fun decrypt(stored: String): String? {
        if (!stored.startsWith(PREFIX)) return stored // legacy plaintext
        return try {
            val raw = Base64.decode(stored.substring(PREFIX.length), Base64.NO_WRAP)
            val iv = raw.copyOfRange(0, IV_LEN)
            val ct = raw.copyOfRange(IV_LEN, raw.size)
            val cipher = Cipher.getInstance(TRANSFORM)
            cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(TAG_BITS, iv))
            String(cipher.doFinal(ct), Charsets.UTF_8)
        } catch (_: Exception) {
            null // key invalidated / corrupt — force re-auth, don't return garbage
        }
    }
}
