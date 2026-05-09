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
import android.net.ConnectivityManager;
import android.net.LinkAddress;
import android.net.Network;
import android.net.wifi.WifiManager;
import android.os.Build;
import android.os.IBinder;
import android.os.PowerManager;
import android.util.Log;
import androidx.core.app.NotificationCompat;
import java.util.List;
import torrplay.App;
import torrplay.Torrplay;

public class TorrPlayService extends Service {
    private static final int NOTIFICATION_ID = 1001;
    private static final String CHANNEL_ID = "torrplay_channel";

    private App torrplayApp;
    private WifiManager.MulticastLock multicastLock;
    private PowerManager.WakeLock wakeLock;
    private volatile boolean isAppRunning = false;

    @Override
    public void onCreate() {
        super.onCreate();
        Log.i("TorrPlayService", "Service onCreate");
        createNotificationChannel();

        try {
            String dataDir = getFilesDir().getAbsolutePath();
            String ipAddress = "0.0.0.0";
            int port = -1;
            Log.i("TorrPlayService", "Initializing TorrPlay with data dir: " + dataDir + ", IP: " + ipAddress + ", Port: " + port);
            torrplayApp = Torrplay.new_(dataDir, ipAddress, port);
        } catch (Exception e) {
            Log.e("TorrPlayService", "Failed to initialize TorrPlay app", e);
            torrplayApp = null;
        }

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

        if (torrplayApp == null) {
            Log.e("TorrPlayService", "TorrPlay app not initialized. Stopping service.");
            stopSelf();
            return START_NOT_STICKY;
        }

        if (isAppRunning) {
            Log.i("TorrPlayService", "TorrPlay app is already running.");
            return START_STICKY;
        }

        startForeground(NOTIFICATION_ID, getNotification());

        new Thread(() -> {
            try {
                isAppRunning = true;
                Log.i("TorrPlayService", "Starting TorrPlay app");
                torrplayApp.start();
                Log.i("TorrPlayService", "TorrPlay app has stopped.");
            } catch (Exception e) {
                Log.e("TorrPlayService", "TorrPlay app crashed", e);
            } finally {
                isAppRunning = false;
            }
        }).start();

        return START_STICKY;
    }

    @Override
    public void onDestroy() {
        super.onDestroy();
        Log.e("TorrPlayService", "Service onDestroy. The service is being killed! Stopping app and scheduling restart...");

        if (torrplayApp != null) {
            try {
                torrplayApp.stop();
                Log.i("TorrPlayService", "TorrPlay app stopped successfully.");
            } catch (Exception e) {
                Log.e("TorrPlayService", "Failed to stop TorrPlay app", e);
            }
        }

        if (wakeLock != null && wakeLock.isHeld()) {
            wakeLock.release();
            wakeLock = null;
        }
        if (multicastLock != null) {
            multicastLock.release();
            multicastLock = null;
        }

        Intent broadcastIntent = new Intent(this, Restarter.class);
        broadcastIntent.setAction(Restarter.ACTION_RESTART_SERVICE);
        this.sendBroadcast(broadcastIntent);
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
