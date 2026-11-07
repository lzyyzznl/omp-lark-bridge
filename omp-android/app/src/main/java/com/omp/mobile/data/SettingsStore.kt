package com.omp.mobile.data

import android.content.Context
import android.content.SharedPreferences
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map

private val Context.dataStore by preferencesDataStore(name = "omp_settings")

data class AppSettings(
    val baseUrl: String = "",
    val deviceId: String = "",
    val deviceName: String = "",
    val mgmtToken: String = "",
    val monitoring: Boolean = true,
) {
    val enrolled: Boolean get() = baseUrl.isNotEmpty() && deviceId.isNotEmpty()
}

class SettingsStore(private val context: Context) {
    private object Keys {
        val baseUrl = stringPreferencesKey("base_url")
        val deviceId = stringPreferencesKey("device_id")
        val deviceName = stringPreferencesKey("device_name")
        val mgmtToken = stringPreferencesKey("mgmt_token")
        val monitoring = booleanPreferencesKey("monitoring")
    }

    private val secure: SharedPreferences by lazy {
        val key = MasterKey.Builder(context)
            .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
            .build()
        EncryptedSharedPreferences.create(
            context,
            "omp_secure",
            key,
            EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
            EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
        )
    }

    val settings: Flow<AppSettings> = context.dataStore.data.map { prefs ->
        AppSettings(
            baseUrl = prefs[Keys.baseUrl].orEmpty(),
            deviceId = prefs[Keys.deviceId].orEmpty(),
            deviceName = prefs[Keys.deviceName].orEmpty(),
            mgmtToken = prefs[Keys.mgmtToken].orEmpty(),
            monitoring = prefs[Keys.monitoring] ?: true,
        )
    }

    suspend fun current(): AppSettings = settings.first()

    suspend fun saveServer(baseUrl: String, mgmtToken: String) {
        context.dataStore.edit { prefs ->
            prefs[Keys.baseUrl] = baseUrl
            prefs[Keys.mgmtToken] = mgmtToken
        }
    }

    suspend fun saveDevice(id: String, name: String, secret: String) {
        context.dataStore.edit { prefs ->
            prefs[Keys.deviceId] = id
            prefs[Keys.deviceName] = name
        }
        secure.edit().putString(SECRET_KEY, secret).apply()
    }

    suspend fun saveMonitoring(enabled: Boolean) {
        context.dataStore.edit { prefs -> prefs[Keys.monitoring] = enabled }
    }

    suspend fun clearDevice() {
        context.dataStore.edit { prefs ->
            prefs.remove(Keys.deviceId)
            prefs.remove(Keys.deviceName)
        }
        secure.edit().remove(SECRET_KEY).apply()
    }

    fun secret(): String = secure.getString(SECRET_KEY, "").orEmpty()

    companion object {
        private const val SECRET_KEY = "device_secret"

        /** Normalizes user input to a Retrofit base URL ending with '/'. */
        fun normalizeBaseUrl(raw: String): String {
            var url = raw.trim()
            if (url.isEmpty()) return ""
            if (!url.startsWith("http://") && !url.startsWith("https://")) {
                url = "http://$url"
            }
            if (!url.endsWith("/")) url += "/"
            return url
        }
    }
}
