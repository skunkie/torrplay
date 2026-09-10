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
    @Override
    public void onReceive(Context context, Intent intent) {
        if (intent == null) return;

        Log.d("Restarter", "Broadcast received with action: " + intent.getAction());

        if (Intent.ACTION_BOOT_COMPLETED.equals(intent.getAction()) || "android.intent.action.QUICKBOOT_POWERON".equals(intent.getAction())) {
            Log.i("Restarter", "Boot completed. Starting TorrPlayService.");
            Intent serviceIntent = new Intent(context, TorrPlayService.class);
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                context.startForegroundService(serviceIntent);
            } else {
                context.startService(serviceIntent);
            }
        }
    }
}
