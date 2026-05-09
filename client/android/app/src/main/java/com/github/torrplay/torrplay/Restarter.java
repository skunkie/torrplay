// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package com.github.torrplay.torrplay;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.os.Build;
import android.util.Log;

public class Restarter extends BroadcastReceiver {

    public static final String ACTION_RESTART_SERVICE = "com.github.torrplay.torrplay.ACTION_RESTART_SERVICE";

    @Override
    public void onReceive(Context context, Intent intent) {
        Log.d("Restarter", "Broadcast received with action: " + intent.getAction());

        if (Intent.ACTION_BOOT_COMPLETED.equals(intent.getAction()) || ACTION_RESTART_SERVICE.equals(intent.getAction())) {
            Log.i("Restarter", "Boot completed or service restart requested. Starting TorrPlayService.");
            Intent serviceIntent = new Intent(context, TorrPlayService.class);
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                context.startForegroundService(serviceIntent);
            } else {
                context.startService(serviceIntent);
            }
        }
    }
}
