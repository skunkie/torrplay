// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

'use client';

import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';

export function ReleaseNotesMarkdown({
  body,
  onOpenLink,
}: {
  body: string,
  onOpenLink: (url: string) => void
}) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      components={{
        h1: ({ children }) => <h3 className='text-base font-semibold mt-3 mb-2 first:mt-0'>{children}</h3>,
        h2: ({ children }) => <h3 className='text-sm font-semibold mt-3 mb-2 first:mt-0'>{children}</h3>,
        h3: ({ children }) => <h3 className='text-sm font-semibold mt-3 mb-2 first:mt-0'>{children}</h3>,
        p: ({ children }) => <p className='my-2 first:mt-0 last:mb-0'>{children}</p>,
        ul: ({ children }) => <ul className='my-2 list-disc pl-5 space-y-1'>{children}</ul>,
        ol: ({ children }) => <ol className='my-2 list-decimal pl-5 space-y-1'>{children}</ol>,
        blockquote: ({ children }) => (
          <blockquote className='my-2 border-l-2 border-border pl-3 text-muted-foreground'>
            {children}
          </blockquote>
        ),
        code: ({ children }) => (
          <code className='rounded bg-muted px-1 py-0.5 font-mono text-[0.9em]'>
            {children}
          </code>
        ),
        pre: ({ children }) => (
          <pre className='my-2 overflow-x-auto rounded-md bg-muted p-3 font-mono'>
            {children}
          </pre>
        ),
        a: ({ href, children }) => (
          <a
            href={href}
            className='font-medium text-primary underline underline-offset-2'
            onClick={event => {
              event.preventDefault();
              if (href) onOpenLink(href);
            }}
          >
            {children}
          </a>
        ),
        table: ({ children }) => (
          <div className='my-2 overflow-x-auto'>
            <table className='w-full border-collapse text-left'>{children}</table>
          </div>
        ),
        th: ({ children }) => (
          <th className='border border-border bg-muted px-2 py-1 font-semibold'>{children}</th>
        ),
        td: ({ children }) => <td className='border border-border px-2 py-1'>{children}</td>,
      }}
    >
      {body}
    </ReactMarkdown>
  );
}
