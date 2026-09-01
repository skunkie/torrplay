// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import VideoPlayer, { type VideoPlayerProps } from './video-player';

type DemoVideoPlayerProps = Omit<VideoPlayerProps, 'internalOnly'>;

const DemoVideoPlayer: React.FC<DemoVideoPlayerProps> = props => {
  return <VideoPlayer {...props}
    internalOnly />;
};

export default DemoVideoPlayer;
