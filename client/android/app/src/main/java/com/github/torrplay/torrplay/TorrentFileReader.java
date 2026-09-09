// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package com.github.torrplay.torrplay;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;

final class TorrentFileReader {
    private TorrentFileReader() {}

    static byte[] read(InputStream inputStream, int maxBytes) throws IOException {
        if (maxBytes < 0) {
            throw new IllegalArgumentException("maxBytes must not be negative");
        }

        ByteArrayOutputStream output = new ByteArrayOutputStream(Math.min(maxBytes, 8192));
        byte[] buffer = new byte[8192];
        int total = 0;
        int count;
        while ((count = inputStream.read(buffer)) != -1) {
            total += count;
            if (total > maxBytes) {
                throw new IOException("Torrent file exceeds the " + maxBytes + " byte limit");
            }
            output.write(buffer, 0, count);
        }
        return output.toByteArray();
    }
}
