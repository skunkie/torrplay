// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package com.github.torrplay.torrplay;

import static androidx.test.platform.app.InstrumentationRegistry.getInstrumentation;
import static org.junit.Assert.assertTrue;

import android.app.ActivityManager;
import android.content.ComponentName;
import android.content.Context;
import android.os.Build;
import android.os.SystemClock;

import androidx.test.core.app.ActivityScenario;
import androidx.test.ext.junit.runners.AndroidJUnit4;
import androidx.test.filters.SdkSuppress;
import androidx.test.uiautomator.By;
import androidx.test.uiautomator.UiDevice;
import androidx.test.uiautomator.UiObject2;
import androidx.test.uiautomator.Until;

import org.junit.Before;
import org.junit.Test;
import org.junit.runner.RunWith;

@RunWith(AndroidJUnit4.class)
public class PermissionFlowTest {

    private UiDevice device;
    private Context context;

    @Before
    public void setUp() {
        context = getInstrumentation().getTargetContext();
        device = UiDevice.getInstance(getInstrumentation());
    }

    @Test
    @SdkSuppress(minSdkVersion = Build.VERSION_CODES.TIRAMISU)
    public void testPermissionFlowStartsService() {
        try (ActivityScenario<MainActivity> scenario = ActivityScenario.launch(MainActivity.class)) {
            // Handle Notification Permission
            handlePermissionIfShown("permission_allow_button_holder", "Allow");

            assertTrue("TorrPlayService should be running", waitForService(TorrPlayService.class, 5000));
        }
    }

    @Test
    @SdkSuppress(maxSdkVersion = Build.VERSION_CODES.S_V2)
    public void testLegacyStoragePermissionFlowStartsService() {
        try (ActivityScenario<MainActivity> scenario = ActivityScenario.launch(MainActivity.class)) {
            handlePermissionIfShown("permission_allow_button_holder", "Allow");

            assertTrue("TorrPlayService should be running", waitForService(TorrPlayService.class, 5000));
        }
    }

    private void handlePermissionIfShown(String resourceId, String buttonText) {
        UiObject2 allowButton = device.wait(Until.findObject(By.res("com.android.permissioncontroller", resourceId)), 2000);
        if (allowButton != null) {
            allowButton.click();
        } else {
            // If not found by resourceId, try by text
            UiObject2 buttonWithText = device.wait(Until.findObject(By.text(buttonText)), 2000);
            if (buttonWithText != null) {
                buttonWithText.click();
            }
        }
    }

    @SuppressWarnings("deprecation")
    private boolean waitForService(Class<?> serviceClass, long timeoutMs) {
        ActivityManager manager = (ActivityManager) context.getSystemService(Context.ACTIVITY_SERVICE);
        ComponentName expected = new ComponentName(context, serviceClass);
        long deadline = SystemClock.elapsedRealtime() + timeoutMs;

        do {
            for (ActivityManager.RunningServiceInfo service : manager.getRunningServices(Integer.MAX_VALUE)) {
                if (expected.equals(service.service)) {
                    return true;
                }
            }
            SystemClock.sleep(100);
        } while (SystemClock.elapsedRealtime() < deadline);

        return false;
    }
}
