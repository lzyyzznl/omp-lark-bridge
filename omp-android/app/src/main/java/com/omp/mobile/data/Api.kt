package com.omp.mobile.data

import com.jakewharton.retrofit2.converter.kotlinx.serialization.asConverterFactory
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import okhttp3.Interceptor
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okhttp3.logging.HttpLoggingInterceptor
import retrofit2.Retrofit
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.PATCH
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query
import java.util.concurrent.TimeUnit
import javax.crypto.Mac
import javax.crypto.spec.SecretKeySpec
import okio.Buffer
import java.security.MessageDigest

/**
 * Every management endpoint answers `{"ok":bool,"data":…}`; failures carry
 * `{"ok":false,"error":"…"}`. Unwrapping is centralized in [Envelope.data].
 */
@Serializable
data class Envelope<T>(
    val ok: Boolean = false,
    val error: String? = null,
    val data: T? = null,
)

interface OmpApi {
    @GET("api/v1/mobile/status")
    suspend fun status(): Envelope<MobileStatus>

    @POST("api/v1/mobile/enroll")
    suspend fun enroll(@Body body: EnrollRequest): Envelope<EnrollResponse>

    @GET("api/v1/mobile/devices")
    suspend fun devices(): Envelope<DevicesResponse>

    @POST("api/v1/mobile/devices/{id}/revoke")
    suspend fun revokeDevice(@Path("id") id: String): retrofit2.Response<Unit>

    @GET("api/v1/mobile/approvals")
    suspend fun approvals(@Query("status") status: String = "all"): Envelope<ApprovalsResponse>

    @POST("api/v1/mobile/approvals/{id}")
    suspend fun decide(@Path("id") id: String, @Body body: DecideRequest): Envelope<DecideResponse>

    @POST("api/v1/mobile/access/grant")
    suspend fun grant(@Body body: GrantRequest): Envelope<GrantResponse>

    @GET("api/v1/hub/nodes")
    suspend fun nodes(): Envelope<NodesResponse>

    @POST("api/v1/hub/pair/new")
    suspend fun pairNew(@Body body: PairNewRequest): Envelope<PairNewResponse>

    @DELETE("api/v1/hub/pair/{device}")
    suspend fun unpair(@Path("device") device: String): retrofit2.Response<Unit>

    @PATCH("api/v1/hub/nodes/{device}")
    suspend fun updateNode(
        @Path("device") device: String,
        @Body body: NodeUpdateRequest,
    ): Envelope<NodeUpdateResponse>
}

/**
 * Current credentials snapshot. The interceptor rereads this on every request,
 * so enroll/login changes apply without rebuilding the client.
 */
data class Creds(val deviceId: String, val secret: String)

class ApiHolder {
    @Volatile var baseUrl: String = ""
    @Volatile var creds: Creds? = null
    @Volatile var mgmtToken: String = ""

    /** Signs every request with the enrolled device secret (HMAC-SHA256). */
    private val signer = Interceptor { chain ->
        val creds = creds
        var request = chain.request()
        if (creds != null) {
            val body = request.body?.let { b ->
                Buffer().also { b.writeTo(it) }.readByteArray()
            } ?: ByteArray(0)
            val ts = (System.currentTimeMillis() / 1000).toString()
            val uri = buildString {
                append(request.url.encodedPath)
                request.url.encodedQuery?.let { append('?').append(it) }
            }
            val canonical = "${request.method}\n$uri\n$ts\n${sha256Hex(body)}"
            request = request.newBuilder()
                .header(HEADER_DEVICE, creds.deviceId)
                .header(HEADER_TS, ts)
                .header(HEADER_SIG, hmacSig(creds.secret, canonical))
                .build()
        }
        if (creds == null && mgmtToken.isNotEmpty()) {
            request = request.newBuilder()
                .header("Authorization", "Bearer $mgmtToken")
                .build()
        }
        chain.proceed(request)
    }

    val client: OkHttpClient = OkHttpClient.Builder()
        .addInterceptor(signer)
        .addInterceptor(HttpLoggingInterceptor().apply {
            level = HttpLoggingInterceptor.Level.BASIC
        })
        .pingInterval(30, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .build()

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        encodeDefaults = false
    }

    private fun retrofit(base: String): Retrofit = Retrofit.Builder()
        .baseUrl(base)
        .client(client)
        .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
        .build()

    @Volatile private var cachedBase: String = ""
    @Volatile private var cachedApi: OmpApi? = null

    fun api(): OmpApi {
        val base = baseUrl
        check(base.isNotEmpty()) { "server URL is not configured" }
        val current = cachedApi
        if (current != null && cachedBase == base) return current
        return synchronized(this) {
            if (cachedApi == null || cachedBase != base) {
                cachedBase = base
                cachedApi = retrofit(base).create(OmpApi::class.java)
            }
            cachedApi!!
        }
    }

    /** Builds the signed WebSocket request for the mobile event stream. */
    fun streamRequest(): Request {
        val base = baseUrl
        check(base.isNotEmpty()) { "server URL is not configured" }
        val builder = Request.Builder().url(base + "api/v1/mobile/stream")
        val creds = creds
        if (creds != null) {
            val ts = (System.currentTimeMillis() / 1000).toString()
            val uri = "/api/v1/mobile/stream"
            val canonical = "GET\n$uri\n$ts\n${sha256Hex(ByteArray(0))}"
            builder
                .header(HEADER_DEVICE, creds.deviceId)
                .header(HEADER_TS, ts)
                .header(HEADER_SIG, hmacSig(creds.secret, canonical))
        } else if (mgmtToken.isNotEmpty()) {
            builder.header("Authorization", "Bearer $mgmtToken")
        }
        return builder.build()
    }

    fun emptyJsonBody(): okhttp3.RequestBody = "".toRequestBody("application/json".toMediaType())

    companion object {
        const val HEADER_DEVICE = "X-OMP-Mobile-Device"
        const val HEADER_TS = "X-OMP-Mobile-Ts"
        const val HEADER_SIG = "X-OMP-Mobile-Sig"

        fun sha256Hex(bytes: ByteArray): String =
            MessageDigest.getInstance("SHA-256").digest(bytes).toHex()

        /**
         * HMAC-SHA256 over the canonical request, base64url without padding —
         * matches mobileSign() in core/mobile.go.
         */
        fun hmacSig(secret: String, canonical: String): String {
            val mac = Mac.getInstance("HmacSHA256")
            mac.init(SecretKeySpec(secret.toByteArray(), "HmacSHA256"))
            return android.util.Base64.encodeToString(
                mac.doFinal(canonical.toByteArray()),
                android.util.Base64.URL_SAFE or
                    android.util.Base64.NO_WRAP or
                    android.util.Base64.NO_PADDING,
            )
        }

        private fun ByteArray.toHex(): String {
            val out = StringBuilder(size * 2)
            for (b in this) out.append("%02x".format(b))
            return out.toString()
        }
    }
}
