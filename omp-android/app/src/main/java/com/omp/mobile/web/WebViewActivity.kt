package com.omp.mobile.web

import android.annotation.SuppressLint
import android.content.Context
import android.content.Intent
import android.content.pm.ApplicationInfo
import android.graphics.Color
import android.os.Bundle
import android.view.Gravity
import android.view.ViewGroup
import android.webkit.CookieManager
import android.webkit.WebChromeClient
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.TextView
import androidx.activity.ComponentActivity
import androidx.activity.OnBackPressedCallback

/**
 * Thin shell around the ompweb UI of one node.
 *
 * The node pages (chat + management) are the web app served by the hub, so the
 * app only provides a window, cookies and navigation. This lives in its own
 * Activity instead of a Compose AndroidView: a WebView hosted inside the
 * ComposeView tree on Android 17/ZTE was laid out with a zero-height Blink
 * viewport (100vh = 0, everything clipped) so nothing was ever painted.
 */
class WebViewActivity : ComponentActivity() {

    private lateinit var webView: WebView
    private lateinit var titleView: TextView
    private lateinit var progress: ProgressBar

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        val url = intent.getStringExtra(EXTRA_URL).orEmpty()
        val title = intent.getStringExtra(EXTRA_TITLE).orEmpty()

        val root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setBackgroundColor(BG)
        }

        val bar = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            setBackgroundColor(BAR_BG)
            setPadding(dp(4), dp(4), dp(4), dp(4))
        }

        val back = TextView(this).apply {
            text = "\u2039"
            textSize = 26f
            setTextColor(FG)
            setPadding(dp(12), 0, dp(12), dp(2))
            setOnClickListener { navigateBack() }
        }

        titleView = TextView(this).apply {
            text = title
            textSize = 16f
            setTextColor(FG)
            maxLines = 1
            ellipsize = android.text.TextUtils.TruncateAt.END
            setPadding(dp(8), 0, dp(8), 0)
        }

        val reload = TextView(this).apply {
            text = "\u21bb"
            textSize = 20f
            setTextColor(FG)
            setPadding(dp(12), 0, dp(12), 0)
            setOnClickListener { webView.reload() }
        }

        bar.addView(back, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        bar.addView(titleView, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        bar.addView(reload, LinearLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT))

        progress = ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal).apply {
            max = 100
            progress = 0
            visibility = ProgressBar.GONE
        }

        val content = FrameLayout(this)
        webView = WebView(this).apply {
            layoutParams = FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT,
            )
            setBackgroundColor(BG)
        }
        content.addView(webView)

        root.addView(bar, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        root.addView(progress, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(3)))
        root.addView(content, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))
        setContentView(root)

        configure(webView)
        if (url.isNotEmpty()) webView.loadUrl(url)

        onBackPressedDispatcher.addCallback(this, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() = navigateBack()
        })
    }

    @SuppressLint("SetJavaScriptEnabled")
    private fun configure(view: WebView) {
        val debuggable = (applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE) != 0
        if (debuggable) WebView.setWebContentsDebuggingEnabled(true)
        CookieManager.getInstance().setAcceptCookie(true)
        CookieManager.getInstance().setAcceptThirdPartyCookies(view, true)

        view.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            databaseEnabled = true
            useWideViewPort = true
            loadWithOverviewMode = true
            cacheMode = WebSettings.LOAD_DEFAULT
            mediaPlaybackRequiresUserGesture = false
            mixedContentMode = WebSettings.MIXED_CONTENT_ALWAYS_ALLOW
        }

        view.webViewClient = object : WebViewClient() {
            override fun onPageFinished(v: WebView, u: String) {
                titleView.text = v.title?.takeIf { it.isNotBlank() } ?: titleView.text
            }
        }

        view.webChromeClient = object : WebChromeClient() {
            override fun onProgressChanged(v: WebView, newProgress: Int) {
                progress.progress = newProgress
                progress.visibility = if (newProgress in 1..99) ProgressBar.VISIBLE else ProgressBar.GONE
            }

            override fun onReceivedTitle(v: WebView, t: String?) {
                t?.takeIf { it.isNotBlank() }?.let { titleView.text = it }
            }
        }
    }

    private fun navigateBack() {
        if (::webView.isInitialized && webView.canGoBack()) {
            webView.goBack()
        } else {
            finish()
        }
    }

    private fun dp(v: Int): Int = (v * resources.displayMetrics.density).toInt()

    companion object {
        private const val EXTRA_URL = "url"
        private const val EXTRA_TITLE = "title"
        private val BG = Color.parseColor("#0C0F11")
        private val BAR_BG = Color.parseColor("#121517")
        private val FG = Color.parseColor("#E7EDF3")

        fun intent(context: Context, url: String, title: String): Intent =
            Intent(context, WebViewActivity::class.java)
                .putExtra(EXTRA_URL, url)
                .putExtra(EXTRA_TITLE, title)
    }
}
