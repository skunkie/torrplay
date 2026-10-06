// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package com.github.torrplay.torrplay;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.net.wifi.WifiManager;
import android.os.Build;
import android.os.IBinder;
import android.os.PowerManager;
import android.util.Log;

import androidx.core.app.NotificationCompat;

import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

import torrplay.App;
import torrplay.Torrplay;

public class TorrPlayService extends Service {
    private static final int NOTIFICATION_ID = 1001;
    private static final String CHANNEL_ID = "torrplay_channel";

    private App torrplayApp;
    private WifiManager.MulticastLock multicastLock;
    private PowerManager.WakeLock wakeLock;
    private final Object lifecycleLock = new Object();
    private final ExecutorService appExecutor = Executors.newSingleThreadExecutor();
    private boolean startScheduled;
    private boolean destroyed;

    @Override
    public void onCreate() {
        super.onCreate();
        Log.i("TorrPlayService", "Service onCreate");
        createNotificationChannel();
        startForeground(NOTIFICATION_ID, getNotification());

        PowerManager powerManager = (PowerManager) getSystemService(POWER_SERVICE);
        if (powerManager != null) {
            wakeLock = powerManager.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "TorrPlay::WakeLock");
            wakeLock.acquire();
        }

        WifiManager wifi = (WifiManager) getApplicationContext().getSystemService(Context.WIFI_SERVICE);
        if (wifi != null) {
            multicastLock = wifi.createMulticastLock("multicastLock");
            multicastLock.setReferenceCounted(true);
            multicastLock.acquire();
        }
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        Log.i("TorrPlayService", "Service onStartCommand");
        scheduleAppStart();

        return START_STICKY;
    }

    private void scheduleAppStart() {
        synchronized (lifecycleLock) {
            if (destroyed || startScheduled) {
                return;
            }
            startScheduled = true;
        }
        appExecutor.execute(this::initializeAndStartApp);
    }

    private void initializeAndStartApp() {
        App app;
        try {
            String dataDir = getFilesDir().getAbsolutePath();
            String ipAddress = "0.0.0.0";
            int port = -1;
            Log.i("TorrPlayService", "Initializing TorrPlay with data dir: " + dataDir + ", IP: " + ipAddress + ", Port: " + port);
            app = Torrplay.new_(dataDir, ipAddress, port);
        } catch (Exception e) {
            Log.e("TorrPlayService", "Failed to initialize TorrPlay app", e);
            stopSelf();
            return;
        }

        boolean discardApp = false;
        try {
            synchronized (lifecycleLock) {
                if (destroyed) {
                    discardApp = true;
                } else {
                    torrplayApp = app;
                    Log.i("TorrPlayService", "Starting TorrPlay app");
                    app.start();
                }
            }
            if (discardApp) {
                stopApp(app, "Failed to stop TorrPlay app after service destruction");
                return;
            }
            Log.i("TorrPlayService", "TorrPlay app started successfully.");
        } catch (Exception e) {
            Log.e("TorrPlayService", "Failed to start TorrPlay app", e);
            boolean ownsApp;
            synchronized (lifecycleLock) {
                ownsApp = torrplayApp == app;
                if (ownsApp) {
                    torrplayApp = null;
                }
            }
            if (ownsApp) {
                stopApp(app, "Failed to clean up TorrPlay app after startup failure");
            }
            stopSelf();
        }
    }

    @Override
    public void onDestroy() {
        Log.i("TorrPlayService", "Service onDestroy. Stopping TorrPlay app.");

        App app;
        synchronized (lifecycleLock) {
            destroyed = true;
            app = torrplayApp;
            torrplayApp = null;
        }
        appExecutor.shutdownNow();

        if (app != null) {
            stopApp(app, "Failed to stop TorrPlay app");
        }

        if (wakeLock != null && wakeLock.isHeld()) {
            wakeLock.release();
            wakeLock = null;
        }
        if (multicastLock != null && multicastLock.isHeld()) {
            multicastLock.release();
            multicastLock = null;
        }

        stopForeground(STOP_FOREGROUND_REMOVE);
        super.onDestroy();
    }

    private void stopApp(App app, String errorMessage) {
        try {
            app.stop();
            Log.i("TorrPlayService", "TorrPlay app stopped successfully.");
        } catch (Exception e) {
            Log.e("TorrPlayService", errorMessage, e);
        }
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }

    private Notification getNotification() {
        return new NotificationCompat.Builder(this, CHANNEL_ID)
                .setContentTitle("TorrPlay Service")
                .setContentText("Running in background")
                .setSmallIcon(R.mipmap.ic_launcher)
                .setPriority(NotificationCompat.PRIORITY_LOW)
                .build();
    }

    private void createNotificationChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            NotificationChannel channel = new NotificationChannel(
                    CHANNEL_ID,
                    "TorrPlay Service",
                    NotificationManager.IMPORTANCE_LOW
            );
            NotificationManager manager = getSystemService(NotificationManager.class);
            if (manager != null) {
                manager.createNotificationChannel(channel);
            }
        }
    }
}
