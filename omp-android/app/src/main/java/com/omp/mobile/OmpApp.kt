package com.omp.mobile

import android.app.Application
import android.webkit.WebView
import com.omp.mobile.data.AppDatabase
import com.omp.mobile.data.Repository
import com.omp.mobile.data.SettingsStore

class OmpApp : Application() {
    lateinit var settings: SettingsStore
        private set
    lateinit var database: AppDatabase
        private set
    lateinit var repository: Repository
        private set

    override fun onCreate() {
        super.onCreate()
        // Debug builds: expose the node WebView to chrome://inspect over adb.
        WebView.setWebContentsDebuggingEnabled(true)
        settings = SettingsStore(this)
        database = AppDatabase.build(this)
        repository = Repository(this, settings, database)
    }
}
