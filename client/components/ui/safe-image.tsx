// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import { ImageOff } from 'lucide-react';
import Image, { type ImageProps } from 'next/image';
import { useEffect, useState } from 'react';

const SafeImage = (props: ImageProps) => {
  const { src, alt, ...rest } = props;
  const [hasError, setHasError] = useState(!src);

  useEffect(() => {
    setHasError(!src);
  }, [src]);

  if (hasError) {
    return (
      <div className='w-full h-full flex items-center justify-center'>
        <ImageOff className='h-16 w-16 text-muted-foreground' />
      </div>
    );
  }

  return <Image {...rest}
    src={src}
    alt={alt}
    onError={() => setHasError(true)} />;
};

export default SafeImage;
